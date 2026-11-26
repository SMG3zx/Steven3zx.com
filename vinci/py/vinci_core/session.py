"""One command's working set, and the helpers every command group shares.

A command reads the state folded once at start, checks its arguments, and appends a few
events with record(). The state is never refolded within a command.

A mistake in what was typed is raised as UserError, which the CLI maps to exit code 1.
Any other error is a bug or a broken journal: stderr, exit 3.

Port of mojo/src/session.mojo.
"""

from __future__ import annotations

import sys

from .journal import Event, JournalTx
from .state import Idea, Note, Prediction, State, state_from
from .text import num, pad_right
from .timeutil import Instant


class UserError(Exception):
    """A mistake in what was typed: printed as "vinci: <message>", exit 1."""


def user_error(message: str) -> UserError:
    return UserError(message)


def is_space(cp: int) -> bool:
    """Whether a code point is white space, by the Unicode White_Space set."""
    if cp == 32 or 9 <= cp <= 13:
        return True
    if cp in (0x85, 0xA0, 0x1680):
        return True
    if 0x2000 <= cp <= 0x200A:
        return True
    return cp in (0x2028, 0x2029, 0x202F, 0x205F, 0x3000)


def collapse_spaces(text: str) -> str:
    """Split on whitespace and join with one space, as Arg does."""
    out: list[str] = []
    pending = False
    for ch in text:
        if is_space(ord(ch)):
            pending = True
        else:
            if pending and len(out) > 0:
                out.append(" ")
            pending = False
            out.append(ch)
    return "".join(out)


def is_number_text(text: str) -> bool:
    """True when text is what NumberStyles.Float accepts: optional white space, an optional
    sign, digits with at most one point (at least one digit), an optional exponent with
    digits, then optional white space. Only ASCII."""
    b = text.encode("utf-8")
    n = len(b)
    if any(c >= 128 for c in b):
        return False
    ws = (32, 9, 10, 11, 12, 13)
    i = 0
    while i < n and b[i] in ws:
        i += 1
    end = n
    while end > i and b[end - 1] in ws:
        end -= 1
    if i < end and b[i] in (43, 45):
        i += 1
    digits = 0
    while i < end and 48 <= b[i] <= 57:
        i += 1
        digits += 1
    if i < end and b[i] == 46:
        i += 1
        while i < end and 48 <= b[i] <= 57:
            i += 1
            digits += 1
    if digits == 0:
        return False
    if i < end and b[i] in (101, 69):
        i += 1
        if i < end and b[i] in (43, 45):
            i += 1
        exp_digits = 0
        while i < end and 48 <= b[i] <= 57:
            i += 1
            exp_digits += 1
        if exp_digits == 0:
            return False
    return i == end


def number(text: str, name: str) -> float:
    """A finite number in plain or exponent notation, read with no locale. Otherwise a
    user error."""
    if is_number_text(text):
        v = float(text.strip())
        if v == v and v not in (float("inf"), float("-inf")):
            return v
    raise user_error("<" + name + "> must be a number, got '" + text + "'")


def pad_left(s: str, width: int) -> str:
    """Right-aligned: spaces before, never cut. Width counts UTF-16 units."""
    from .text import utf16_length

    return " " * (width - utf16_length(s)) + s


def note_line(n: Note) -> str:
    """NoteLine: id, text, and the refs and tags in brackets when there are any. A source
    ref carries the place inside it, as s1:p.42."""
    parts: list[str] = []
    for r in n.refs:
        if r.startswith("s") and n.where_ is not None:
            parts.append(r + ":" + n.where_)
        else:
            parts.append(r)
    for t in n.tags:
        parts.append("+" + t)
    line = pad_right(n.id, 4) + " " + n.text
    if len(parts) > 0:
        line += "  [" + " ".join(parts) + "]"
    return line


def prediction_line(h: Prediction) -> str:
    """PredictionLine: id, verdict (right, wrong or "open "), confidence, claim."""
    verdict = "open "
    if h.right is not None:
        verdict = "right" if h.right else "wrong"
    return pad_right(h.id, 4) + " " + verdict + " " + pad_left(num(h.confidence), 3) + "%  " + h.text


class Session:
    """The journal, the state folded once at start, and the clock for one command."""

    def __init__(self, journal: JournalTx, now: Instant) -> None:
        """Reads the journal once, under the transaction's lock. A broken journal fails here,
        even for help. The caller commits or aborts the transaction."""
        self.state: State = state_from(journal.read())
        self.journal: JournalTx = journal
        self.now: Instant = now
        # The lines the window and git tracker wrote before this command (track.py). `vinci
        # sync` prints them; every other command ignores them.
        self.tracked: list[str] = []

    def record(self, e: Event) -> None:
        """Queue one fact for the transaction. The state is not refolded; later reads in this
        command see the old one."""
        self.journal.append(e)

    def commit(self) -> None:
        """Writes the command's facts with one fsync and releases the lock."""
        self.journal.commit()

    def abort(self) -> None:
        """Drops the command's facts and releases the lock, after an error."""
        self.journal.abort()

    def read_input(self) -> str:
        """Everything on standard input: what a lone "-" argument means."""
        return sys.stdin.read()

    def arg(self, a: list[str], index: int, name: str) -> str:
        """Arg: the argument at index, whitespace collapsed. "-" reads standard input.
        Missing or empty is a user error that names <name>."""
        if index >= len(a):
            raise user_error("missing <" + name + ">")
        value = self.read_input() if a[index] == "-" else a[index]
        collapsed = collapse_spaces(value)
        if len(collapsed.encode("utf-8")) == 0:
            raise user_error("<" + name + "> is empty")
        return collapsed

    def idea_arg(self, a: list[str]) -> Idea:
        """IdeaArg: the first argument must name an idea. Returns it."""
        id = self.arg(a, 0, "idea id")
        i = self.state.idea_index(id)
        if i < 0:
            raise user_error("no idea " + id)
        return self.state.ideas[i]

    def next_id(self, prefix: str) -> str:
        """NextId: the next id for a prefix letter, from the folded state (i1, r2, ...)."""
        return self.state.next_id(prefix)

    def describe(self, id: str) -> str:
        """Describe: the note, prediction or idea line for an id."""
        n = self.state.note_index(id)
        if n >= 0:
            return note_line(self.state.notes[n])
        h = self.state.prediction_index(id)
        if h >= 0:
            return prediction_line(self.state.predictions[h])
        i = self.state.idea_index(id)
        if i < 0:
            raise ValueError("describe: no idea, note or prediction " + id)
        return pad_right(id, 4) + " " + self.state.ideas[i].title

    def section(self, title: str, lines: list[str]) -> None:
        """Section: a title and indented lines, printed only when there are lines."""
        if len(lines) == 0:
            return
        print(title)
        for line in lines:
            print("  " + line)
