# `vinci` and `vinci chat` in Mojo: the conversation with the chat model (src/Chat/ChatCommand.cs
# and src/Chat/Conversation.cs). The model is llama-server's OpenAI-compatible
# /v1/chat/completions. The tools are in chat_tools.mojo. The citation check is in chat_cite.mojo.
#
# A turn: the model either asks for a tool, whose result it reads next, or it answers. The
# answer's citations are checked against this turn's tool results, and each exchange is written
# to the journal as an `exchange` line. An answer is kept only when it cites evidence the tools
# returned, and every document passage it cites has a source. Neo4j is not involved.
#
# The summarize step of the plan is not here: the sweep showed the chat model reads 2.5k tokens.

from std.python import Python, PythonObject
from std.sys import exit

from chat_action import Action, parse_action, summary_of, trim_bytes
from chat_cite import (
    cited_ids_with_evidence,
    document_base,
    is_document_cite,
    unseen_cites,
)
from chat_edge import complete, read_line
from chat_tools import CHAT_INSTRUCTIONS, ChatTools, chat_index_path, next_id
from dates import today_of
from embed_edge import env_or
from index_build import update_from_journal
from index_store import IndexStore, py_params
from jsonline import json_string
from journal import Event, Journal
from session import Session
from cmd_ideas import dispatch_ideas
from text import ascii_tail
from timeutil import Instant, format_instant

comptime MAX_ROUNDS = 6
comptime DEFAULT_HISTORY_CHARS = 24000
comptime MESSAGE_OVERHEAD = 40
comptime DEFAULT_BASE_URL = "http://127.0.0.1:8080/v1"
comptime DEFAULT_MODEL = "swift-qwen3.8-27b"
comptime CHAT_MODEL_ERROR = "chatmodel:"
comptime NOT_ANSWERED = (
    "I could not reach an answer in the steps I had. Ask it more narrowly."
)
comptime SYSTEM_HEAD = """You are the assistant inside vinci, a personal system for ideas, notes, predictions, measurements and time.
The person you are talking to keeps their knowledge, goals and work in it.
"""
comptime SYSTEM_RULES = """How to work:
- For anything about their ideas, notes, sources, goals, predictions, metrics, commits or time, use the journal. Never guess what it holds.
- For questions about the past, use recall first. Answer only from what recall returned, and cite each fact as [id] exactly as it appears there.
- If a recall line ends with (open <id> for more) and the answer needs the rest of it, use open with that id.
- Passages from documents are cited the same way, as [d1/2]. Do not write a sources list: vinci adds one from the titles of the documents.
- If recall finds nothing relevant, say you do not know. Do not fill the gap.
- When the person states a durable fact or preference, save it with remember.
- When asked to record or change something, do it with the journal, then say in one line what you recorded.
- Be brief. Plain sentences. No headings unless asked.
"""


@fieldwise_init
struct Message(Copyable, Movable):
    var role: String
    var content: String


def system_prompt(today: String) -> String:
    """The system prompt, with the tool instructions. Today's date is the local date.
    """
    return (
        SYSTEM_HEAD
        + "\n"
        + tool_instructions()
        + "\n\n"
        + SYSTEM_RULES
        + "\nToday is "
        + today
        + "."
    )


def tool_instructions() -> String:
    return String(CHAT_INSTRUCTIONS)


def messages_json(system: String, recent: List[Message]) raises -> String:
    """The messages as the chat endpoint takes them: the system prompt, then the history.
    """
    var out = '[{"role":"system","content":' + json_string(system) + "}"
    for m in recent:
        out += (
            ',{"role":'
            + json_string(m.role)
            + ',"content":'
            + json_string(m.content)
            + "}"
        )
    return out + "]"


def digits_of_stamp(now: Instant) raises -> String:
    """yyyyMMddHHmmss from an instant's own clock, for the session id."""
    var b = format_instant(now).as_bytes()
    var out = List[UInt8]()
    for i in range(19):
        if b[i] >= 48 and b[i] <= 57:
            out.append(b[i])
    return String(unsafe_from_utf8=out)


def history_limit() raises -> Int:
    """CHAT_HISTORY_CHARS, the most history sent each turn. Default 24000."""
    try:
        return Int(env_or("CHAT_HISTORY_CHARS", String(DEFAULT_HISTORY_CHARS)))
    except:
        return DEFAULT_HISTORY_CHARS


def stderr_line(text: String):
    """One line on standard error. Nothing to do if Python cannot write it."""
    try:
        var sys = Python.import_module("sys")
        sys.stderr.write(text + "\n")
    except:
        pass


def pipeline(journal_path: String, now: Instant) raises:
    """`vinci pipeline`, run in this process: it prints to standard output."""
    var s = Session(Journal(journal_path), now.copy())
    var rest = List[String]()
    _ = dispatch_ideas(s, "pipeline", rest)


struct Chat(Movable):
    """One conversation: the model, the history, and the tools it can call."""

    var tools: ChatTools
    var base_url: String
    var model: String
    var think: Bool
    var history_chars: Int
    var session_id: String
    var now: Instant
    var journal_path: String
    var index_path: String
    var history: List[Message]

    def __init__(
        out self,
        var tools: ChatTools,
        var base_url: String,
        var model: String,
        think: Bool,
        history_chars: Int,
        var session_id: String,
        var now: Instant,
        var journal_path: String,
        var index_path: String,
    ):
        self.tools = tools^
        self.base_url = base_url^
        self.model = model^
        self.think = think
        self.history_chars = history_chars
        self.session_id = session_id^
        self.now = now^
        self.journal_path = journal_path^
        self.index_path = index_path^
        self.history = List[Message]()

    def recent(self) raises -> List[Message]:
        """The newest messages that fit the history budget, starting on a user message.
        """
        var newest_first = List[Message]()
        var size = 0
        var i = len(self.history) - 1
        while i >= 0:
            var length = (
                self.history[i].content.byte_length() + MESSAGE_OVERHEAD
            )
            if size + length > self.history_chars:
                break
            size += length
            newest_first.append(self.history[i].copy())
            i -= 1
        var start = len(newest_first) - 1
        while start >= 0 and newest_first[start].role != "user":
            start -= 1
        var out = List[Message]()
        for j in range(start, -1, -1):
            out.append(newest_first[j].copy())
        return out^

    def ask(self) raises -> String:
        """One model call with the system prompt and the recent history. Raises with the
        CHAT_MODEL_ERROR marker when the model cannot be reached or refuses."""
        var today = today_of(self.now.copy())
        var recent = self.recent()
        var body = messages_json(system_prompt(today), recent)
        var status = complete(self.base_url, self.model, body, self.think)
        if not status.ok:
            raise Error(CHAT_MODEL_ERROR + status.text)
        return trim_bytes(status.text, False)

    def turn(mut self, user_text: String) raises -> String:
        """One user message and the answer to it, after any tool calls."""
        self.history.append(Message("user", user_text))
        var evidence = String()
        var answer = String()
        var answered = False
        var rounds = 0
        while rounds < MAX_ROUNDS and not answered:
            rounds += 1
            var reply = self.ask()
            var parsed = parse_action(reply)
            if parsed is None:
                answer = reply.copy()
                self.history.append(Message("assistant", answer))
                answered = True
                break
            var action = parsed.value().copy()
            self.history.append(Message("assistant", reply))
            var result = self.tools.run(action)
            evidence += result + "\n"
            print("  · " + action.name + " " + summary_of(action))
            self.history.append(
                Message("user", "Result of " + action.name + ":\n" + result)
            )
        if not answered:
            answer = String(NOT_ANSWERED)

        var cited = cited_ids_with_evidence(answer, evidence)
        var unsourced = unseen_cites(cited, evidence)
        for c in unsourced:
            print(
                "  ! cites [" + c + "], which recall did not return this turn"
            )
        var sources = List[String]()
        var unattributed = List[String]()
        var store_needed = False
        for c in cited:
            if is_document_cite(c):
                store_needed = True
        if store_needed:
            var store = IndexStore(self.index_path)
            for c in cited:
                if not is_document_cite(c):
                    continue
                var source_text = source_of(store, c)
                if source_text.byte_length() == 0:
                    unattributed.append(c.copy())
                elif source_text not in sources:
                    sources.append(source_text.copy())
        for c in unattributed:
            print(
                "  ! cites ["
                + c
                + "], a passage with no source. The answer is not saved."
            )
        if len(sources) > 0:
            answer += "\n\nSources:"
            for source_text in sources:
                answer += "\n- " + source_text
        var keep = (
            len(cited) > 0 and len(unsourced) == 0 and len(unattributed) == 0
        )
        self.record("user", user_text, "user_message")
        if keep:
            self.record("assistant", answer, "agent_message")
        else:
            print("  · answer not saved: it cites no recalled evidence")
        return answer^

    def record(mut self, who: String, text: String, kind: String) raises:
        """One exchange line: who said it, the text, the session, and the message kind.
        """
        var id = next_id(self.journal_path, "e")
        var tags = List[String]()
        tags.append(kind)
        Journal(self.journal_path).append(
            Event(
                self.now.copy(),
                "exchange",
                id=Optional[String](id),
                text=Optional[String](text),
                who=Optional[String](who),
                why=Optional[String](self.session_id),
                tags=Optional[List[String]](tags^),
            )
        )
        self.tools.index_update()


def source_of(store: IndexStore, cite: String) raises -> String:
    """The source of a passage: its document's title and locator, as the citation list shows
    them. Empty when the document is not in the index."""
    var row = store.conn.execute(
        (
            "SELECT text FROM facts WHERE kind = 'read' AND cite = ? AND"
            " valid_until IS NULL AND status = 'active' ORDER BY rowid DESC"
            " LIMIT 1"
        ),
        py_params(PythonObject(document_base(cite))),
    ).fetchone()
    if Bool(row == Python.none()):
        return String()
    var text = String(row[0])
    var lines = text.split("\n")
    var title = String(lines[0])
    var locator = String()
    if len(lines) > 1:
        locator = String(lines[1])
    if locator.byte_length() == 0:
        return '"' + title + '"'
    return '"' + title + '", ' + locator


def run_chat(
    home: String,
    journal_path: String,
    now: Instant,
    exe: String,
) raises:
    """`vinci` and `vinci chat`: the prompt loop. Ends at end of input or /exit.
    """
    var base_url = env_or("CHAT_BASE_URL", DEFAULT_BASE_URL)
    var model = env_or("CHAT_MODEL", DEFAULT_MODEL)
    var think = env_or("CHAT_THINK", "") == "1"
    var index_path = chat_index_path(home)
    var tools = ChatTools(
        home.copy(),
        journal_path.copy(),
        exe.copy(),
        now.copy(),
        index_path.copy(),
    )
    var chat = Chat(
        tools^,
        base_url.copy(),
        model.copy(),
        think,
        history_limit(),
        "vinci-chat-" + digits_of_stamp(now),
        now.copy(),
        journal_path.copy(),
        index_path^,
    )
    # The index follows the journal before the first question (JournalSync's place).
    chat.tools.index_update()
    print(
        "vinci · chat model "
        + model
        + " at "
        + base_url
        + " · /help for commands, /exit to leave"
    )
    pipeline(journal_path, now)
    print("")
    while True:
        print("you> ", end="")
        var raw = read_line()
        if raw is None:
            break
        var line = trim_bytes(raw.value(), False)
        if line == "/exit" or line == "/quit":
            break
        if line.byte_length() == 0:
            continue
        if line == "/help":
            print(
                "Ask anything, or tell it what to do. It uses the journal, the"
                " index and the model's own answers."
            )
            print("/pipeline  show the stages      /exit  leave")
            continue
        if line == "/pipeline":
            pipeline(journal_path, now)
            continue
        try:
            var answer = chat.turn(line)
            print("")
            print(answer)
            print("")
        except e:
            var message = String(e)
            if message.startswith(CHAT_MODEL_ERROR):
                stderr_line("vinci: " + String(ascii_tail(message, 10)))
                stderr_line(
                    "The chat model is at "
                    + base_url
                    + ". Start it with: systemctl --user start llama-chat,"
                    " or set"
                    " CHAT_BASE_URL."
                )
                exit(1)
            raise Error(message)
