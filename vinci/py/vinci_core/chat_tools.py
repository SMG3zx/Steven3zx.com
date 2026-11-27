"""The chat's tools, over the journal and the index. Each tool returns the text the model reads
next, and never raises: a failure is "error: <message>".

  journal   one vinci command, run as a child process so its output is captured
  recall    the index, packed under a budget (default 3000 tokens, whole 10)
  open      part of one id: lines=A-B or grep=word, capped in total
  remember  a `remember` journal line, then an index update
  read      a text file or web page: its passages become a body file under docs/, with a `read`
            journal line, then an index update

Writes go through the write confirmation (chat_gate.py) once untrusted text is in the
conversation. Reading untrusted text sets the flag: read, and any recall or open that returns a
passage not written by the person.

Port of mojo/src/chat_tools.mojo.
"""

from __future__ import annotations

import os
from typing import Optional

from vinci_edge import chat_edge, reading_edge

from .paths import join_path
from .chat_action import (
    Action,
    compact_json,
    has_arg,
    int_arg,
    list_arg,
    optional_text_arg,
    text_arg,
)
from .chat_gate import ask_write, is_write_call, needs_confirmation, refusal
from .command_kinds import chat_runs
from .dates import today_of
from .embed_config import default_config
from .handles import lookup
from .index_build import update_from_journal
from .index_store import CITE_MATCH, IndexStore
from .journal import Event, JournalTx, next_id_of
from .jsonline import json_string
from .open_view import Request, parse_lines_arg
from .pack import estimate_counter, pack
from .chat_open import open_text
from .recall import Settings as RecallSettings
from .recall import feed_hint_line, parse_sources, recall
from .timeutil import Instant, format_instant, parse_instant

DEFAULT_BUDGET = 3000
# The newest whole records within the budget; the rest are pointers (the M3 sweep, budget 3000).
CHAT_WHOLE = 10
RECALL_LIMIT = 10
# Eval only (the M4 chat-loop evaluation). VINCI_CHAT_RECALL_BUDGET and VINCI_CHAT_RECALL_WHOLE set
# the packing of recall; VINCI_CHAT_INDEX names the index file the chat reads, and then the chat
# never updates it from the journal. Unset, none of them changes anything.
ENV_RECALL_BUDGET = "VINCI_CHAT_RECALL_BUDGET"
ENV_RECALL_WHOLE = "VINCI_CHAT_RECALL_WHOLE"
ENV_INDEX = "VINCI_CHAT_INDEX"
# Eval only (the M4 step d evaluation): VINCI_CHAT_UPFRONT=0 turns off the recall that runs on the
# person's own words before the first model call of each turn (chat.py).
ENV_UPFRONT = "VINCI_CHAT_UPFRONT"
FALLBACK_NOTE = "note: the embedding server did not answer, so this recall is lexical only.\n"
CHAT_INSTRUCTIONS = """You have five tools. To use one, reply with only a JSON object and nothing else:
{"tool": "journal", "args": ["idea", "Flying machine"]}
{"tool": "recall", "query": "what did I say about gliders"}
{"tool": "recall", "query": "when did the index change", "sources": "git"}
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
recall returns one line per fact, and each line starts with its id in brackets, such as [i1] or [d1/2]. A line that ends with (open <id> for more) is cut: open that id to read the rest.
recall searches only the journal unless you give "sources". Use "git" for the monorepo's commits: what changed in the code, and when. Use "vaults" for the notes folders: Meingrad, Research and the vinci docs. Use "git,vaults" for both, or "all" for the journal and both feeds. With "all", the journal's lines come first and up to three feed lines follow. A line from git is labelled "git commit", and one from the notes "vault section". Those are not the person's own writing: cite them as [id] like any line, and say where they came from. If recall abstains and says that feed records may match, ask again with "sources": "git,vaults" before you answer that nothing is known.
open shows part of one id: lines=A-B for a range of lines, or grep=word for the lines that contain a word. Without either it shows the whole fact. Its output is cut at 4000 bytes.
remember saves a durable fact about the person. Its category is one of fact, preference, decision, procedure, episode.
read saves a text file (.txt, .md) or a web page that the person named, so recall can find its passages. Use it only when the person gives you the path or the address.
When a write is refused, the tool result says so. Say that nothing was written.
After a tool runs you will see its result. When you have what you need, reply in plain sentences instead of JSON."""
CATEGORIES = " fact preference decision procedure episode "


def routes() -> list[str]:
    """The routes a recall runs: lexical and vector, as the default of `vinci recall`."""
    return ["lexical", "vector"]


def env_int(name: str, fallback: int) -> int:
    """An integer from the environment, or fallback when it is unset."""
    text = os.environ.get(name, "")
    if text == "":
        return fallback
    try:
        return int(text)
    except ValueError:
        raise ValueError(name + " must be a whole number") from None


def upfront_enabled() -> bool:
    """True unless VINCI_CHAT_UPFRONT is 0: the chat recalls on the person's words first."""
    return os.environ.get(ENV_UPFRONT, "1") != "0"


def chat_index_path(home: str) -> str:
    """The index the chat reads: home/index.sqlite, or VINCI_CHAT_INDEX (eval only)."""
    return os.environ.get(ENV_INDEX, join_path(home, "index.sqlite"))


class ChatTools:
    """The tools and what they share: the paths, the clock, and the untrusted flag."""

    def __init__(self, home: str, journal_path: str, exe: str, now: Instant, index_path: str) -> None:
        self.index_path: str = index_path
        self.home: str = home
        self.journal_path: str = journal_path
        self.exe: str = exe
        self.now: Instant = now
        self.untrusted: bool = False

    def append_minted(
        self,
        letter: str,
        kind: str,
        at: Optional[Instant] = None,
        text: Optional[str] = None,
        tags: Optional[list[str]] = None,
        url: Optional[str] = None,
        where_: Optional[str] = None,
    ) -> str:
        """One journal line with the next free id for letter, minted and written under one lock.
        The line is at self.now unless at is given."""
        tx = JournalTx(self.journal_path)
        tx.begin()
        try:
            ident = next_id_of(tx.read(), letter)
            when = at if at is not None else self.now
            tx.append(Event(when, kind, id=ident, text=text, tags=tags, url=url, where_=where_))
            tx.commit()
            return ident
        except BaseException:
            tx.abort()
            raise

    def index_update(self) -> None:
        """The index follows the journal. Not under VINCI_CHAT_INDEX, where the index is a fixed
        file (the evaluation)."""
        if os.environ.get(ENV_INDEX, "") != "":
            return
        store = IndexStore(self.index_path)
        update_from_journal(store, self.journal_path, self.home)

    def allowed(self, name: str, action: Action) -> bool:
        """The write confirmation. True when the call may run. A refusal is printed as the
        person's answer, and the model gets the refusal text as the result."""
        args: list[str] = []
        if name == "journal" and has_arg(action, "args"):
            args = list_arg(action, "args")
        if not needs_confirmation(self.untrusted, is_write_call(name, args)):
            return True
        return ask_write(name + " " + compact_json(action))

    def run(self, action: Action) -> str:
        """The result of one tool call, or an error line."""
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
            return "error: there is no tool named " + action.name + ". Use journal, recall, open, remember or read."
        except Exception as err:
            return "error: " + str(err)

    def journal_tool(self, action: Action) -> str:
        if has_arg(action, "args"):
            args = list_arg(action, "args")
        else:
            raise ValueError("args must be a list of strings")
        if len(args) == 0:
            args.append("help")
        command = args[0]
        # The table (command_kinds.py) says which words are journal commands. A route, or a word the
        # table does not know, is refused before anything runs.
        if not chat_runs(command):
            return "error: vinci: unknown command '" + command + "'. Run 'vinci help'."
        if not self.allowed("journal", action):
            return refusal()
        json_args = "[" + ",".join(json_string(a) for a in args) + "]"
        # A read-only command runs without the tracker: the tracker writes spent, work and used
        # lines, and only a command the person allowed may write (command_kinds.py).
        code, text = chat_edge.run_vinci(self.exe, json_args, not is_write_call("journal", args))
        text = text.rstrip()
        if is_write_call("journal", args):
            self.index_update()
        if code == 0:
            return text if text != "" else "done"
        return "error: " + text

    def recall_tool(self, action: Action) -> str:
        query = text_arg(action, "query")
        budget = int_arg(action, "budget", env_int(ENV_RECALL_BUDGET, DEFAULT_BUDGET))
        if budget < 1:
            raise ValueError("budget must be at least 1")
        named = optional_text_arg(action, "sources")
        sources = parse_sources(named) if named is not None else ["journal"]
        return self.recall_query(query, budget, sources)

    def upfront_recall(self, query: str) -> str:
        """The recall that runs before the first model call of a turn, on the person's own words,
        packed as the recall tool packs. Empty when it cannot run: the model can still call recall
        itself, so nothing is raised."""
        try:
            return self.recall_query(query, env_int(ENV_RECALL_BUDGET, DEFAULT_BUDGET), ["journal"])
        except Exception:
            return ""

    def recall_query(self, query: str, budget: int, sources: list[str]) -> str:
        """recall: the index searched for query in the named sources, packed under budget, with the
        lexical fallback when the embedding server cannot answer."""
        store = IndexStore(self.index_path)
        settings = RecallSettings(RECALL_LIMIT, routes(), self.now.utc, today_of(self.now), default_config())
        settings.sources = list(sources)
        try:
            return self.recall_text(store, query, settings, budget, "")
        except Exception:
            # Without the embedding server the lexical route still answers. The note says so.
            settings.routes = ["lexical"]
            return self.recall_text(store, query, settings, budget, FALLBACK_NOTE)

    def recall_text(self, store: IndexStore, query: str, settings: RecallSettings, budget: int, note: str) -> str:
        """Recall and pack: the text the model reads, with any abstention reason first."""
        found = recall(store, query, settings)
        whole = env_int(ENV_RECALL_WHOLE, CHAT_WHOLE)
        packed = pack(found.results, budget, whole, estimate_counter())
        out = note
        if found.abstention.abstain:
            out += "abstain: " + found.abstention.reason + "\n"
        if found.feed_hint > 0:
            out += feed_hint_line(found.feed_hint) + "\n"
        if packed.text == "":
            out += "No memory matches that."
        else:
            out += packed.text
        for c in found.results:
            if not c.trusted and ("[" + c.id + "]") in packed.text:
                self.untrusted = True
        return out

    def open_tool(self, action: Action) -> str:
        ident = text_arg(action, "id")
        req = Request()
        lines = optional_text_arg(action, "lines")
        grep = optional_text_arg(action, "grep")
        if lines is not None:
            parse_lines_arg(lines, req)
        if grep is not None:
            if grep == "":
                raise ValueError("grep needs a word")
            req.has_grep = True
            req.word = grep
        if req.has_lines and req.has_grep:
            raise ValueError("give lines= or grep=, not both")
        store = IndexStore(self.index_path)
        items = lookup(store, ident)
        if len(items) == 0:
            raise ValueError("no fact or handle named '" + ident + "' in the index")
        if self.any_untrusted(store, ident):
            self.untrusted = True
        return open_text(items, ident, req)

    def any_untrusted(self, store: IndexStore, ident: str) -> bool:
        """True when a fact under this citation came from text that is not the person's."""
        row = store.conn.execute(
            "SELECT COUNT(*) FROM facts WHERE " + CITE_MATCH + " AND trusted = 0", (ident, ident)
        ).fetchone()
        return int(row[0]) > 0

    def remember_tool(self, action: Action) -> str:
        content = text_arg(action, "content")
        category = "fact"
        given = optional_text_arg(action, "category")
        if given is not None and (" " + given + " ") in CATEGORIES:
            category = given
        if not self.allowed("remember", action):
            return refusal()
        ident = self.append_minted("k", "remember", text=content, tags=[category])
        self.index_update()
        return "remembered " + ident

    def read_tool(self, action: Action) -> str:
        path = optional_text_arg(action, "path")
        url = optional_text_arg(action, "url")
        if (path is None) == (url is None):
            raise ValueError("read needs either a path or a url, not both")
        target = path if path is not None else url
        assert target is not None
        if not self.allowed("read", action):
            return refusal()
        result = reading_edge.read_source(target, url is not None, self.home, format_instant(self.now))
        if result["status"] != "ok":
            raise ValueError(str(result["error"]))
        title = str(result["title"])
        locator = str(result["locator"])
        date = str(result["date"])
        count = str(result["count"])
        where_ = str(result["where"])
        at = parse_instant(date + "T00:00:00Z")
        ident = self.append_minted("d", "read", at=at, text=title, url=locator, where_=where_)
        self.index_update()
        self.untrusted = True
        return (
            'read "' + title + '" from ' + locator + " on " + date + ": " + count
            + " passages saved as " + ident + ". Ask recall about it to get the passages."
        )
