"""The command table against the dispatch sources. Every word the dispatch tables compare with is in
the table, and every table word is compared somewhere, so a new command cannot ship unclassified.
The chat's decisions (what it may run, what writes) are checked on the table's own answers."""

import re
from pathlib import Path

import pytest

from vinci_core.chat_gate import is_write_call
from vinci_core.command_kinds import (
    chat_runs,
    command_table,
    command_writes_journal,
    find_command,
    journal_command_words,
)

CORE = Path(__file__).resolve().parents[1] / "vinci_core"
CLI = Path(__file__).resolve().parents[1] / "vinci_cli" / "vinci.py"
JOURNAL_SOURCES = [CORE / "cmd_ideas.py", CORE / "cmd_knowledge.py", CORE / "cmd_ops.py"]
QUOTED = re.compile(r'"([^"]*)"')


def dispatch_words() -> list[str]:
    """The journal words the dispatch functions compare with: cmd == "word" or cmd in ("word", ...)."""
    words: list[str] = []
    for path in JOURNAL_SOURCES:
        for line in path.read_text(encoding="utf-8").splitlines():
            if "cmd ==" in line or "cmd in (" in line:
                for word in QUOTED.findall(line):
                    if word and word not in words:
                        words.append(word)
    return words


def route_words() -> list[str]:
    """The words vinci.py routes before the journal: args[0] == "word"."""
    words: list[str] = []
    for line in CLI.read_text(encoding="utf-8").splitlines():
        if "args[0] ==" in line or "args[0] in" in line:
            for word in QUOTED.findall(line):
                if word and word not in words:
                    words.append(word)
    return words


def test_every_dispatched_word_is_classified() -> None:
    seen = dispatch_words()
    assert len(seen) > 40, "the dispatch sources yielded too few words"
    for w in seen:
        assert find_command(w) >= 0, "dispatch word '" + w + "' is not in command_kinds"
    for w in route_words():
        assert find_command(w) >= 0, "routed word '" + w + "' is not in command_kinds"


def test_every_table_word_is_dispatched() -> None:
    """A table word that no dispatch compares with is stale: the table would then allow a command
    that does not exist."""
    seen = dispatch_words()
    routed = route_words()
    for w in journal_command_words():
        assert w in seen, "table word '" + w + "' is not compared in a journal dispatch"
    for k in command_table():
        if k.route:
            assert k.name in routed or k.name == "chat", "table route '" + k.name + "' is not routed"


def test_chat_fence_and_gate() -> None:
    assert chat_runs("idea")
    assert chat_runs("ideas")
    assert chat_runs("help")
    assert not command_writes_journal("ideas")
    assert not command_writes_journal("show")
    assert command_writes_journal("idea")
    assert command_writes_journal("sync")
    assert command_writes_journal("review")
    for w in ["chat", "serve", "index", "feed", "recall", "open", "backup", "verify"]:
        assert not chat_runs(w), "the chat must refuse '" + w + "'"
    assert not chat_runs("nosuch")
    assert command_writes_journal("nosuch"), "an unknown word is a write"
    assert not is_write_call("journal", ["ideas"])
    assert is_write_call("journal", ["spent"])


@pytest.mark.parametrize(
    "word",
    ["help", "ideas", "pipeline", "next", "show", "notes", "tags", "sources",
     "predictions", "stats", "issues", "principles", "bookmarks", "watches", "path"],
)
def test_writes_match_the_old_chat_list(word: str) -> None:
    """The read-only words the chat allowed before the table existed. They must all still be
    read-only, so the gate does not start asking about them."""
    assert not command_writes_journal(word), "'" + word + "' must only read"
