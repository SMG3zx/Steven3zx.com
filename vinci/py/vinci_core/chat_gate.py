"""The write confirmation.

Once untrusted text has entered the chat's context (the read tool saved a document, or a recall or
open returned a passage that is not the person's own journal line), a tool call that writes is
printed and needs a yes from the person on standard input before it runs. Anything but y or yes is
a no, and end of input is a no. A no reaches the model as the tool result, so it can say that
nothing was written. Journal-only turns write without asking.

Port of mojo/src/chat_gate.mojo.
"""

from __future__ import annotations

from vinci_edge import chat_edge

from .command_kinds import chat_runs, command_writes_journal

REFUSAL = (
    "refused: the person did not allow this write, so nothing was written."
    " Say so, and do not retry it unless the person asks."
)


def is_read_only_command(command: str) -> bool:
    """True for a journal command that only reads, as command_kinds classes it. Anything else is a
    write: sync (the tracker records time in normal use), review (it appends a review line), and any
    word the table does not know."""
    return chat_runs(command) and not command_writes_journal(command)


def is_write_call(name: str, args: list[str]) -> bool:
    """True when the tool call writes. journal writes unless its command only reads; remember and
    read write. recall and open only read. An unknown name is not a write: the tool result says
    there is no such tool."""
    if name == "journal":
        command = args[0] if len(args) > 0 else "help"
        return not is_read_only_command(command)
    return name in ("remember", "read")


def needs_confirmation(untrusted: bool, write: bool) -> bool:
    """The gate: a write after untrusted text needs a yes."""
    return untrusted and write


def is_yes(answer: str) -> bool:
    """y or yes, in any case, with white space around it. Anything else is a no."""
    word = answer.strip().lower()
    return word in ("y", "yes")


def ask_write(description: str) -> bool:
    """Prints the write that is waiting, and reads the person's answer. True only for a yes. End
    of input is a no, so a piped run without answers writes nothing."""
    print(
        "  ? " + description + " This is a write, and untrusted text is in this"
        " conversation. Allow it? [y/N] ",
        end="",
    )
    line = read_line()
    if line is None:
        print("")
        return False
    return is_yes(line)


def read_line() -> "str | None":
    """One line of standard input without its line ending. None at end of input."""
    status, text = chat_edge.read_line()
    if status == "eof":
        return None
    return text


def refusal() -> str:
    return REFUSAL
