"""`vinci` and `vinci chat`: the conversation with the chat model.

The model is llama-server's OpenAI-compatible /v1/chat/completions. The tools are in chat_tools.py.
The citation check is in chat_cite.py.

A turn: the model either asks for a tool, whose result it reads next, or it answers. The answer's
citations are checked against this turn's tool results, and each exchange is written to the journal
as an `exchange` line. An answer is kept only when it cites evidence the tools returned, and every
document passage it cites has a source.

Port of mojo/src/chat.mojo.
"""

from __future__ import annotations

import os
import sys
from dataclasses import dataclass, field

from vinci_edge import chat_edge

from .chat_action import parse_action, plain_summary, trim_bytes
from .chat_cite import cited_ids_with_evidence, document_base, is_document_cite, unseen_cites
from .chat_gate import read_line
from .chat_tools import CHAT_INSTRUCTIONS, ChatTools, chat_index_path, upfront_enabled
from .cmd_ideas import dispatch_ideas
from .dates import today_of
from .index_store import IndexStore
from .jsonline import json_string
from .journal import Event, JournalTx, next_id_of
from .session import Session
from .timeutil import Instant, format_instant

MAX_ROUNDS = 6
DEFAULT_HISTORY_CHARS = 24000
MESSAGE_OVERHEAD = 40
DEFAULT_BASE_URL = "http://127.0.0.1:8080/v1"
DEFAULT_MODEL = "qwen3.5-9b"
CHAT_MODEL_ERROR = "chatmodel:"
NOT_ANSWERED = "I could not reach an answer in the steps I had. Ask it more narrowly."
SYSTEM_HEAD = """You are the assistant inside vinci, a personal system for ideas, notes, predictions, measurements and time.
The person you are talking to keeps their knowledge, goals and work in it.
"""
SYSTEM_RULES = """How to work:
- For anything about their ideas, notes, sources, goals, predictions, metrics, commits or time, use the journal. Never guess what it holds.
- For questions about the past, use recall first. Answer only from what recall returned, and cite each fact as [id] exactly as it appears there.
- If a recall line ends with (open <id> for more) and the answer needs the rest of it, use open with that id.
- Passages from documents are cited the same way, as [d1/2]. Do not write a sources list: vinci adds one from the titles of the documents.
- If recall finds nothing relevant, say you do not know. Do not fill the gap.
- When the person asks for a suggestion, a recommendation or advice, base it on what the journal says they like, prefer, have done or have said about the subject. Tailor the answer to those facts, cite them, and say which ones you used. A stated preference is an answer to such a question, even when no list of options is stored.
- When the person states a durable fact or preference, save it with remember.
- When asked to record or change something, do it with the journal, then say in one line what you recorded.
- Be brief. Plain sentences. No headings unless asked.
"""


@dataclass
class Message:
    role: str
    content: str


def system_prompt(today: str) -> str:
    """The system prompt, with the tool instructions. Today's date is the local date."""
    return SYSTEM_HEAD + "\n" + tool_instructions() + "\n\n" + SYSTEM_RULES + "\nToday is " + today + "."


def tool_instructions() -> str:
    return CHAT_INSTRUCTIONS


def messages_json(system: str, recent: list[Message]) -> str:
    """The messages as the chat endpoint takes them: the system prompt, then the history."""
    out = '[{"role":"system","content":' + json_string(system) + "}"
    for m in recent:
        out += ',{"role":' + json_string(m.role) + ',"content":' + json_string(m.content) + "}"
    return out + "]"


def digits_of_stamp(now: Instant) -> str:
    """yyyyMMddHHmmss from an instant's own clock, for the session id."""
    return "".join(ch for ch in format_instant(now)[:19] if "0" <= ch <= "9")


def history_limit() -> int:
    """CHAT_HISTORY_CHARS, the most history sent each turn. Default 24000."""
    try:
        return int(os.environ.get("CHAT_HISTORY_CHARS", str(DEFAULT_HISTORY_CHARS)))
    except ValueError:
        return DEFAULT_HISTORY_CHARS


def stderr_line(text: str) -> None:
    """One line on standard error. Nothing to do if Python cannot write it."""
    try:
        sys.stderr.write(text + "\n")
    except OSError:
        pass


DIM = "2"
YELLOW = "33"
HELP = """Ask in plain words, or tell it what to do. It searches the journal, the index and your documents,
and cites what it used as [id].

  /pipeline   show the idea stages
  /help       show this
  /exit       leave (Ctrl+D or Ctrl+C at the prompt also leave)

· lines are tool calls. ! lines are warnings; an answer with a warning may not be saved."""


def paint(text: str, code: str) -> str:
    """The text in an ANSI style when standard output is a terminal and NO_COLOR is not set."""
    if os.environ.get("NO_COLOR", "") != "" or not sys.stdout.isatty():
        return text
    return "\033[" + code + "m" + text + "\033[0m"


def while_thinking(call):
    """Runs call, and while it runs shows 'thinking' on standard error when that is a terminal. The
    note is cleared when call returns or raises."""
    if not sys.stderr.isatty():
        return call()
    sys.stderr.write(paint("  thinking…", DIM))
    sys.stderr.flush()
    try:
        return call()
    finally:
        sys.stderr.write("\r\033[K")
        sys.stderr.flush()


def pipeline(journal_path: str, now: Instant) -> None:
    """`vinci journal pipeline`, run in this process: it prints to standard output."""
    # pipeline only reads, so it takes no lock.
    s = Session(JournalTx(journal_path), now)
    try:
        dispatch_ideas(s, "pipeline", [])
        s.commit()
    except BaseException:
        s.abort()
        raise


def source_of(store: IndexStore, cite: str) -> str:
    """The source of a passage: its document's title and locator, as the citation list shows them.
    Empty when the document is not in the index."""
    row = store.conn.execute(
        "SELECT text FROM facts WHERE kind = 'read' AND cite = ? AND valid_until IS NULL"
        " AND status = 'active' ORDER BY rowid DESC LIMIT 1",
        (document_base(cite),),
    ).fetchone()
    if row is None:
        return ""
    lines = str(row[0]).split("\n")
    title = lines[0]
    locator = lines[1] if len(lines) > 1 else ""
    if locator == "":
        return '"' + title + '"'
    return '"' + title + '", ' + locator


@dataclass
class Chat:
    """One conversation: the model, the history, and the tools it can call."""

    tools: ChatTools
    base_url: str
    model: str
    think: bool
    history_chars: int
    session_id: str
    now: Instant
    journal_path: str
    index_path: str
    history: list[Message] = field(default_factory=list)

    def recent(self) -> list[Message]:
        """The newest messages that fit the history budget, starting on a user message."""
        newest_first: list[Message] = []
        size = 0
        i = len(self.history) - 1
        while i >= 0:
            length = len(self.history[i].content.encode("utf-8")) + MESSAGE_OVERHEAD
            if size + length > self.history_chars:
                break
            size += length
            newest_first.append(self.history[i])
            i -= 1
        start = len(newest_first) - 1
        while start >= 0 and newest_first[start].role != "user":
            start -= 1
        return [newest_first[j] for j in range(start, -1, -1)]

    def ask(self) -> str:
        """One model call with the system prompt and the recent history. Raises with the
        CHAT_MODEL_ERROR marker when the model cannot be reached or refuses."""
        today = today_of(self.now)
        body = messages_json(system_prompt(today), self.recent())
        status, text = chat_edge.complete(self.base_url, self.model, body, self.think)
        if status != "ok":
            raise ValueError(CHAT_MODEL_ERROR + text)
        return trim_bytes(text, False)

    def turn(self, user_text: str) -> str:
        """One user message and the answer to it. If the turn fails (the model is down, or the person
        cancels it), the history goes back to how it was, so the next question starts clean."""
        mark = len(self.history)
        try:
            return self._turn(user_text)
        except BaseException:
            del self.history[mark:]
            raise

    def _turn(self, user_text: str) -> str:
        self.history.append(Message("user", user_text))
        evidence = ""
        answer = ""
        answered = False
        rounds = 0
        if upfront_enabled():
            # Recall on the person's own words first, in the context as a recall call the model
            # made. Its ids are evidence for the citation check, and it is silent.
            found = self.tools.upfront_recall(user_text)
            if len(found) > 0:
                evidence += found + "\n"
                self.history.append(
                    Message("assistant", '{"tool": "recall", "query": ' + json_string(user_text) + "}")
                )
                self.history.append(Message("user", "Result of recall:\n" + found))
        while rounds < MAX_ROUNDS and not answered:
            rounds += 1
            reply = while_thinking(self.ask)
            action = parse_action(reply)
            if action is None:
                answer = reply
                self.history.append(Message("assistant", answer))
                answered = True
                break
            self.history.append(Message("assistant", reply))
            result = self.tools.run(action)
            evidence += result + "\n"
            print(paint("  · " + action.name + " " + plain_summary(action), DIM))
            self.history.append(Message("user", "Result of " + action.name + ":\n" + result))
        if not answered:
            answer = NOT_ANSWERED

        cited = cited_ids_with_evidence(answer, evidence)
        unsourced = unseen_cites(cited, evidence)
        for c in unsourced:
            print(paint("  ! cites [" + c + "], which recall did not return this turn", YELLOW))
        sources: list[str] = []
        unattributed: list[str] = []
        store_needed = any(is_document_cite(c) for c in cited)
        if store_needed:
            store = IndexStore(self.index_path)
            for c in cited:
                if not is_document_cite(c):
                    continue
                source_text = source_of(store, c)
                if source_text == "":
                    unattributed.append(c)
                elif source_text not in sources:
                    sources.append(source_text)
        for c in unattributed:
            print(paint("  ! cites [" + c + "], a passage with no source. The answer is not saved.", YELLOW))
        if len(sources) > 0:
            answer += "\n\nSources:"
            for source_text in sources:
                answer += "\n- " + source_text
        keep = len(cited) > 0 and len(unsourced) == 0 and len(unattributed) == 0
        self.record("user", user_text, "user_message")
        if keep:
            self.record("assistant", answer, "agent_message")
        elif len(cited) == 0:
            # Not a fault: a general answer with no journal evidence is kept out of the journal.
            print(
                paint(
                    "  · answer not saved: it cites nothing recall returned. Ask it more narrowly, or add the fact.",
                    DIM,
                )
            )
        elif len(unsourced) > 0 or len(unattributed) > 0:
            print(paint("  ! answer not saved: a citation above was not checked.", YELLOW))
        return answer

    def record(self, who: str, text: str, kind: str) -> None:
        """One exchange line: who said it, the text, the session, and the message kind."""
        tx = JournalTx(self.journal_path)
        tx.begin()
        try:
            ident = next_id_of(tx.read(), "e")
            tx.append(
                Event(self.now, "exchange", id=ident, text=text, who=who, why=self.session_id, tags=[kind])
            )
            tx.commit()
        except BaseException:
            tx.abort()
            raise
        self.tools.index_update()


def run_chat(home: str, journal_path: str, now: Instant, exe: str) -> None:
    """`vinci` and `vinci chat`: the prompt loop. Ends at end of input or /exit."""
    base_url = os.environ.get("CHAT_BASE_URL", DEFAULT_BASE_URL)
    model = os.environ.get("CHAT_MODEL", DEFAULT_MODEL)
    think = os.environ.get("CHAT_THINK", "") == "1"
    index_path = chat_index_path(home)
    tools = ChatTools(home, journal_path, exe, now, index_path)
    chat = Chat(
        tools,
        base_url,
        model,
        think,
        history_limit(),
        "vinci-chat-" + digits_of_stamp(now),
        now,
        journal_path,
        index_path,
    )
    # The index follows the journal before the first question (JournalSync's place).
    chat.tools.index_update()
    print(
        paint(
            "vinci · chat model " + model + " at " + base_url + " · /help for commands, /exit to leave", DIM
        )
    )
    pipeline(journal_path, now)
    print("")
    try:
        while True:
            print("you> ", end="", flush=True)
            raw = read_line()
            if raw is None:
                break
            line = trim_bytes(raw, False)
            if line == "/exit" or line == "/quit":
                break
            if line == "":
                continue
            if line == "/help":
                print(HELP)
                continue
            if line == "/pipeline":
                pipeline(journal_path, now)
                continue
            try:
                answer = chat.turn(line)
            except KeyboardInterrupt:
                print("\n" + paint("  cancelled. Nothing was saved. Ask again, or /exit.", DIM))
                continue
            except Exception as err:
                message = str(err)
                if not message.startswith(CHAT_MODEL_ERROR):
                    raise
                # The session stays open. Nothing was recorded for this question, so it is asked again by typing it.
                stderr_line("vinci: " + message[len(CHAT_MODEL_ERROR) :])
                stderr_line(
                    "The chat model is at "
                    + base_url
                    + ". Start it with: systemctl --user start vinci-infer, or set CHAT_BASE_URL."
                    + " Your question was not answered; ask it again once the model is up."
                )
                continue
            print("")
            print(answer)
            print("")
    except KeyboardInterrupt:
        print("")
