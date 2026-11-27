"""Every command the vinci binary dispatches, classified as writing the journal or not.

This table is the one place that says what a command may do to the journal. The chat's
`journal` tool reads it twice: a command that is not a journal command (route is True, or the
word is unknown) is refused without running, and a command that writes is gated by the
person's yes once untrusted text is in the conversation (chat_gate.py).

Port of mojo/src/command_kinds.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class CommandKind:
    """One command word, whether it writes the journal, and whether it is routed."""

    name: str
    writes: bool
    route: bool


def command_table() -> list[CommandKind]:
    """The table. A journal command is dispatched by dispatch_ideas, dispatch_knowledge or
    dispatch_ops (vinci.py runs them in that order); a route is handled before the journal."""

    def c(name: str, writes: bool, route: bool = False) -> CommandKind:
        return CommandKind(name, writes, route)

    return [
        # cmd_ideas.py: dispatch_ideas
        c("idea", True), c("for", True), c("goal", True), c("req", True), c("cut", True),
        c("advance", True), c("adopt", True), c("home", True), c("watch", True),
        c("unwatch", True), c("watches", False), c("spent", True), c("ship", True),
        c("used", True), c("tend", True), c("retire", True), c("ideas", False),
        c("pipeline", False), c("next", False),
        # cmd_knowledge.py: dispatch_knowledge
        c("bookmark", True), c("unbookmark", True), c("bookmarks", False), c("source", True),
        c("note", True), c("link", True), c("notes", False), c("tags", False),
        c("sources", False), c("show", False), c("predict", True), c("result", True),
        c("predictions", False), c("log", True), c("spec", True), c("stats", False),
        # cmd_ops.py: dispatch_ops
        c("help", False), c("path", False), c("brief", False),
        c("issue", True), c("diagnose", True), c("principle", True),
        c("issues", False), c("principles", False), c("commit", True), c("kept", True),
        c("missed", True), c("review", True),
        # vinci.py: routed before the journal is read
        c("chat", True, True), c("serve", False, True), c("index", False, True),
        c("feed", False, True), c("recall", False), c("open", False),
        c("backup", False, True), c("verify", False, True),
    ]


def find_command(name: str) -> int:
    """The index of name in command_table(), or -1 when it is not a command."""
    for i, k in enumerate(command_table()):
        if k.name == name:
            return i
    return -1


def command_writes_journal(name: str) -> bool:
    """True when the command appends journal lines. An unknown word counts as a write, so
    the chat asks before it can do anything."""
    i = find_command(name)
    if i < 0:
        return True
    return command_table()[i].writes


# The chat has its own tools for these two lookups, so its journal tool does not run them.
CHAT_OWN_TOOLS: tuple[str, ...] = ("recall", "open")


def chat_runs(name: str) -> bool:
    """True when the chat's journal tool may run this word: a journal command in the table that
    the chat has no tool for. Anything else (a route, a chat tool's word, or a word the table
    does not know) is refused without running."""
    if name in CHAT_OWN_TOOLS:
        return False
    i = find_command(name)
    if i < 0:
        return False
    return not command_table()[i].route


def chat_words() -> list[str]:
    """Every word the chat's journal tool runs, in table order. The chat prompt is built from
    this list, so it cannot drift from what the tool accepts."""
    return [k.name for k in command_table() if chat_runs(k.name)]


def journal_command_words() -> list[str]:
    """The words the journal dispatch handles, in table order."""
    return [k.name for k in command_table() if not k.route]
