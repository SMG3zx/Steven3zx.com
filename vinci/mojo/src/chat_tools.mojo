# The chat's tools (src/Chat/Tools.cs), with the memory service replaced by the journal and the
# index. Each tool returns the text the model reads next, and never raises: a failure is
# "error: <message>", as ChatTools.Run returns it.
#
#   journal   one vinci command, run as a child process so its output is captured as C# does
#   recall    the index, packed under a budget (default 3000 tokens, whole 10)
#   open      part of one id: lines=A-B or grep=word, capped in total
#   remember  a `remember` journal line, then an index update
#   read      a text file or web page: its passages become a body file under docs/, with a
#             `read` journal line, then an index update
#
# Writes go through the write confirmation (chat_gate.mojo) once untrusted text is in the
# conversation. Reading untrusted text sets the flag: read, and any recall or open that
# returns a passage not written by the person.

from std.python import Python, PythonObject

from chat_action import (
    Action,
    compact_json,
    has_arg,
    int_arg,
    list_arg,
    optional_text_arg,
    text_arg,
)
from chat_edge import read_source, run_vinci
from chat_gate import ask_write, is_write_call, needs_confirmation, refusal
from chat_open import open_text
from dates import today_of
from embed_edge import default_config, env_or
from handles import lookup
from index_build import update_from_journal
from index_store import IndexStore, py_params
from jsonline import json_string
from journal import Event, Journal
from open_view import no_request, parse_lines_arg
from pack import estimate_counter, pack
from recall import Settings, recall
from session import USER_PREFIX
from text import ascii_tail
from timeutil import Instant, format_instant, parse_instant

comptime DEFAULT_BUDGET = 3000
# The newest whole records within the budget; the rest are pointers (the M3 sweep, budget 3000).
comptime CHAT_WHOLE = 10
comptime RECALL_LIMIT = 10
# Eval only (the M4 chat-loop evaluation). VINCI_CHAT_RECALL_BUDGET and VINCI_CHAT_RECALL_WHOLE
# set the packing of recall; VINCI_CHAT_INDEX names the index file the chat reads, and then the
# chat never updates it from the journal. Unset, none of them changes anything.
comptime ENV_RECALL_BUDGET = "VINCI_CHAT_RECALL_BUDGET"
comptime ENV_RECALL_WHOLE = "VINCI_CHAT_RECALL_WHOLE"
comptime ENV_INDEX = "VINCI_CHAT_INDEX"
# Eval only (the M4 step d evaluation): VINCI_CHAT_UPFRONT=0 turns off the recall that runs
# on the person's own words before the first model call of each turn (chat.mojo).
comptime ENV_UPFRONT = "VINCI_CHAT_UPFRONT"
comptime FALLBACK_NOTE = (
    "note: the embedding server did not answer, so this recall is lexical"
    " only.\n"
)
comptime CHAT_INSTRUCTIONS = """You have five tools. To use one, reply with only a JSON object and nothing else:
{"tool": "journal", "args": ["idea", "Flying machine"]}
{"tool": "recall", "query": "what did I say about gliders"}
{"tool": "open", "id": "i1"}
{"tool": "open", "id": "i1", "grep": "wing"}
{"tool": "remember", "content": "The person prefers short answers", "category": "preference"}
{"tool": "read", "path": "notes/glider.md"}
{"tool": "read", "url": "https://example.org/article"}

journal runs one vinci command. Its args are the command word, then its arguments, one item each.
To list things, use the plural word: ideas, notes, sources, predictions, issues, principles. show takes one id, such as i1.
Each call runs one command. To do two things, make two calls.
The command words are: idea, for, goal, req, cut, advance, ideas, pipeline, next, show, commit, kept, missed, ship, used, tend, retire,
note, link, notes, tags, source, sources, bookmark, bookmarks, unbookmark, predict, result, predictions, log, spec, stats,
issue, diagnose, principle, issues, principles, review, adopt, home, watch, unwatch, watches, sync, spent, path.
recall searches the journal, notes, sources and what was read. It returns one line per fact, and each line starts with its id in brackets, such as [i1] or [d1/2]. A line that ends with (open <id> for more) is cut: open that id to read the rest.
open shows part of one id: lines=A-B for a range of lines, or grep=word for the lines that contain a word. Without either it shows the whole fact. Its output is cut at 4000 bytes.
remember saves a durable fact about the person. Its category is one of fact, preference, decision, procedure, episode.
read saves a text file (.txt, .md) or a web page that the person named, so recall can find its passages. Use it only when the person gives you the path or the address.
When a write is refused, the tool result says so. Say that nothing was written.
After a tool runs you will see its result. When you have what you need, reply in plain sentences instead of JSON."""
comptime CATEGORIES = " fact preference decision procedure episode "
comptime COMMANDS_NOT_IN_CHAT = " chat serve memory index recall open "


def join_path(dir: String, name: String) -> String:
    if dir.endswith("/"):
        return dir + name
    return dir + "/" + name


def routes() raises -> List[String]:
    """The routes a recall runs: lexical and vector, as the default of `vinci recall`.
    """
    var out = List[String]()
    out.append("lexical")
    out.append("vector")
    return out^


def error_text(e: Error) raises -> String:
    """The message of an error, without the marker a user error carries."""
    var message = String(e)
    if message.startswith(USER_PREFIX):
        return String(ascii_tail(message, 5))
    return message


def env_int(name: String, fallback: Int) raises -> Int:
    """An integer from the environment, or fallback when it is unset."""
    var text = env_or(name, "")
    if text.byte_length() == 0:
        return fallback
    try:
        return Int(text)
    except:
        raise Error(name + " must be a whole number")


def upfront_enabled() raises -> Bool:
    """True unless VINCI_CHAT_UPFRONT is 0: the chat recalls on the person's words first.
    """
    return env_or(ENV_UPFRONT, "1") != "0"


def chat_index_path(home: String) raises -> String:
    """The index the chat reads: home/index.sqlite, or VINCI_CHAT_INDEX (eval only).
    """
    return env_or(ENV_INDEX, join_path(home, "index.sqlite"))


def next_id(journal_path: String, letter: String) raises -> String:
    """The next free id with a prefix letter (k1, e7 ...), from the journal's own ids.
    """
    var best = 0
    for e in Journal(journal_path).read():
        if e.id is None:
            continue
        var id = e.id.value()
        if id.startswith(letter) and id.byte_length() > 1:
            try:
                var n = Int(String(ascii_tail(id, 1)))
                if n > best:
                    best = n
            except:
                pass
    return letter + String(best + 1)


struct ChatTools(Movable):
    """The tools and what they share: the paths, the clock, and the untrusted flag.
    """

    var home: String
    var journal_path: String
    var index_path: String
    var exe: String
    var now: Instant
    var untrusted: Bool

    def __init__(
        out self,
        var home: String,
        var journal_path: String,
        var exe: String,
        var now: Instant,
        var index_path: String,
    ):
        self.index_path = index_path^
        self.home = home^
        self.journal_path = journal_path^
        self.exe = exe^
        self.now = now^
        self.untrusted = False

    def index_update(self) raises:
        """The index follows the journal: this is what replaces JournalSync. Not under
        VINCI_CHAT_INDEX, where the index is a fixed file (the evaluation)."""
        if env_or(ENV_INDEX, "").byte_length() > 0:
            return
        var store = IndexStore(self.index_path)
        _ = update_from_journal(store, self.journal_path, self.home)

    def allowed(self, name: String, action: Action) raises -> Bool:
        """The write confirmation. True when the call may run. A refusal is printed as the
        person's answer, and the model gets the refusal text as the result."""
        var args = List[String]()
        if name == "journal" and has_arg(action, "args"):
            args = list_arg(action, "args")
        if not needs_confirmation(self.untrusted, is_write_call(name, args)):
            return True
        var what = name + " " + compact_json(action)
        return ask_write(what)

    def run(mut self, action: Action) raises -> String:
        """ChatTools.Run: the result of one tool call, or an error line."""
        try:
            if action.name == "journal":
                return self.journal_tool(action)
            if action.name == "recall":
                return self.recall_tool(action)
            if action.name == "open":
                return self.open_tool(action)
            if action.name == "remember":
                return self.remember_tool(action)
            if action.name == "read":
                return self.read_tool(action)
            return (
                "error: there is no tool named "
                + action.name
                + ". Use journal, recall, open, remember or read."
            )
        except e:
            return "error: " + error_text(e)

    def journal_tool(mut self, action: Action) raises -> String:
        var args = List[String]()
        if has_arg(action, "args"):
            args = list_arg(action, "args")
        else:
            raise Error("args must be a list of strings")
        if len(args) == 0:
            args.append("help")
        var command = args[0].copy()
        if (" " + command + " ") in COMMANDS_NOT_IN_CHAT:
            return (
                "error: vinci: unknown command '"
                + command
                + "'. Run 'vinci help'."
            )
        if not self.allowed("journal", action):
            return refusal()
        var json_args = String("[")
        for i in range(len(args)):
            if i > 0:
                json_args += ","
            json_args += json_string(args[i])
        json_args += "]"
        var ran = run_vinci(self.exe, json_args)
        var code = ran[0]
        var text = String(ran[1].rstrip())
        if is_write_call("journal", args):
            self.index_update()
        if code == 0:
            return text if text.byte_length() > 0 else String("done")
        return "error: " + text

    def recall_tool(mut self, action: Action) raises -> String:
        var query = text_arg(action, "query")
        var budget = int_arg(
            action, "budget", env_int(ENV_RECALL_BUDGET, DEFAULT_BUDGET)
        )
        if budget < 1:
            raise Error("budget must be at least 1")
        return self.recall_query(query, budget)

    def upfront_recall(mut self, query: String) raises -> String:
        """The recall that runs before the first model call of a turn, on the person's own
        words, packed as the recall tool packs. Empty when it cannot run: the model can still
        call recall itself, so nothing is raised."""
        try:
            return self.recall_query(
                query, env_int(ENV_RECALL_BUDGET, DEFAULT_BUDGET)
            )
        except:
            return String()

    def recall_query(mut self, query: String, budget: Int) raises -> String:
        """recall: the index searched for query, packed under budget, with the lexical
        fallback when the embedding server cannot answer."""
        var store = IndexStore(self.index_path)
        var settings = Settings(
            RECALL_LIMIT,
            routes(),
            self.now.utc,
            today_of(self.now.copy()),
            default_config(),
        )
        try:
            return self.recall_text(store, query, settings, budget, "")
        except e:
            # Without the embedding server the lexical route still answers. The note says so.
            settings.routes = List[String]()
            settings.routes.append("lexical")
            var note = String(FALLBACK_NOTE)
            return self.recall_text(store, query, settings, budget, note)

    def recall_text(
        mut self,
        store: IndexStore,
        query: String,
        settings: Settings,
        budget: Int,
        note: String,
    ) raises -> String:
        """Recall and pack: the text the model reads, with any abstention reason first.
        """
        var found = recall(store, query, settings)
        var whole = env_int(ENV_RECALL_WHOLE, CHAT_WHOLE)
        var packed = pack(found.results, budget, whole, estimate_counter())
        var out = note.copy()
        if found.abstention.abstain:
            out += "abstain: " + found.abstention.reason + "\n"
        if packed.text.byte_length() == 0:
            out += "No memory matches that."
        else:
            out += packed.text
        for c in found.results:
            if not c.trusted and packed.text.find("[" + c.id + "]") >= 0:
                self.untrusted = True
        return out^

    def open_tool(mut self, action: Action) raises -> String:
        var id = text_arg(action, "id")
        var req = no_request()
        var lines = optional_text_arg(action, "lines")
        var grep = optional_text_arg(action, "grep")
        if lines is not None:
            parse_lines_arg(lines.value(), req)
        if grep is not None:
            if grep.value().byte_length() == 0:
                raise Error("grep needs a word")
            req.has_grep = True
            req.word = grep.value()
        if req.has_lines and req.has_grep:
            raise Error("give lines= or grep=, not both")
        var store = IndexStore(self.index_path)
        var items = lookup(store, id)
        if len(items) == 0:
            raise Error("no fact or handle named '" + id + "' in the index")
        if self.any_untrusted(store, id):
            self.untrusted = True
        return open_text(items, id, req)

    def any_untrusted(self, store: IndexStore, id: String) raises -> Bool:
        """True when a fact under this citation came from text that is not the person's.
        """
        var row = store.conn.execute(
            "SELECT COUNT(*) FROM facts WHERE cite = ? AND trusted = 0",
            py_params(PythonObject(id)),
        ).fetchone()
        return Int(String(row[0])) > 0

    def remember_tool(mut self, action: Action) raises -> String:
        var content = text_arg(action, "content")
        var category = String("fact")
        var given = optional_text_arg(action, "category")
        if given is not None and (" " + given.value() + " ") in CATEGORIES:
            category = given.value()
        if not self.allowed("remember", action):
            return refusal()
        var id = next_id(self.journal_path, "k")
        var tags = List[String]()
        tags.append(category)
        Journal(self.journal_path).append(
            Event(
                self.now.copy(),
                "remember",
                id=Optional[String](id),
                text=Optional[String](content),
                tags=Optional[List[String]](tags^),
            )
        )
        self.index_update()
        return "remembered " + id

    def read_tool(mut self, action: Action) raises -> String:
        var path = optional_text_arg(action, "path")
        var url = optional_text_arg(action, "url")
        if (path is None) == (url is None):
            raise Error("read needs either a path or a url, not both")
        var target = path.value() if path is not None else url.value()
        if not self.allowed("read", action):
            return refusal()
        var result = read_source(
            target,
            url is not None,
            self.home,
            format_instant(self.now.copy()),
        )
        if String(result["status"]) != "ok":
            raise Error(String(result["error"]))
        var title = String(result["title"])
        var locator = String(result["locator"])
        var date = String(result["date"])
        var count = String(result["count"])
        var where_ = String(result["where"])
        var id = next_id(self.journal_path, "d")
        var at = parse_instant(date + "T00:00:00Z")
        Journal(self.journal_path).append(
            Event(
                at^,
                "read",
                id=Optional[String](id),
                text=Optional[String](title),
                url=Optional[String](locator),
                where_=Optional[String](where_),
            )
        )
        self.index_update()
        self.untrusted = True
        return (
            'read "'
            + title
            + '" from '
            + locator
            + " on "
            + date
            + ": "
            + count
            + " passages saved as "
            + id
            + ". Ask recall about it to get the passages."
        )
