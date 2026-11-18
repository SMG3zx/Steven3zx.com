# One command's working set, and the helpers App.cs keeps for every command group.
# The reference is Session and its helpers at the end of src/App.cs.
#
# A command reads the state folded once at start, checks its arguments, and
# appends a few events with record(). The state is never refolded within a
# command, as in C#.
#
# A mistake in what was typed is raised with a marker, which vinci.mojo's main maps to an exit code:
#   user_error("text")     -> "user:text"       stderr "vinci: text", exit 1
# Any other error is a bug or a broken journal: stderr, exit 3.

from std.math import isfinite
from std.python import Python

from journal import Event, JournalTx
from state import Idea, Note, Prediction, State, state_from
from text import (
    code_points_to_string,
    num,
    pad_right,
    utf16_length,
    utf8_code_points,
)
from timeutil import Instant

comptime USER_PREFIX = "user:"


def user_error(message: String) -> Error:
    """A mistake in what was typed: printed as "vinci: <message>", exit 1."""
    return Error(USER_PREFIX + message)


def is_space(cp: Int) -> Bool:
    """Char.IsWhiteSpace, the test string.Split(null) uses in C#."""
    if cp == 32 or (cp >= 9 and cp <= 13):
        return True
    if cp == 0x85 or cp == 0xA0 or cp == 0x1680:
        return True
    if cp >= 0x2000 and cp <= 0x200A:
        return True
    return (
        cp == 0x2028
        or cp == 0x2029
        or cp == 0x202F
        or cp == 0x205F
        or cp == 0x3000
    )


def collapse_spaces(text: String) raises -> String:
    """Split on whitespace and join with one space, as Arg does."""
    var out = List[Int]()
    var pending = False
    for cp in utf8_code_points(text):
        if is_space(cp):
            pending = True
        else:
            if pending and len(out) > 0:
                out.append(32)
            pending = False
            out.append(cp)
    return code_points_to_string(out)


def is_number_text(text: String) -> Bool:
    """True when text is what NumberStyles.Float accepts: optional white space,
    an optional sign, digits with at most one point (at least one digit), an
    optional exponent with digits, then optional white space. Only ASCII."""
    var b = text.as_bytes()
    var n = len(b)
    var i = 0
    while i < n and (Int(b[i]) == 32 or (Int(b[i]) >= 9 and Int(b[i]) <= 13)):
        i += 1
    var end = n
    while end > i and (
        Int(b[end - 1]) == 32
        or (Int(b[end - 1]) >= 9 and Int(b[end - 1]) <= 13)
    ):
        end -= 1
    if i < end and (Int(b[i]) == 43 or Int(b[i]) == 45):
        i += 1
    var digits = 0
    while i < end and Int(b[i]) >= 48 and Int(b[i]) <= 57:
        i += 1
        digits += 1
    if i < end and Int(b[i]) == 46:
        i += 1
        while i < end and Int(b[i]) >= 48 and Int(b[i]) <= 57:
            i += 1
            digits += 1
    if digits == 0:
        return False
    if i < end and (Int(b[i]) == 101 or Int(b[i]) == 69):
        i += 1
        if i < end and (Int(b[i]) == 43 or Int(b[i]) == 45):
            i += 1
        var exp_digits = 0
        while i < end and Int(b[i]) >= 48 and Int(b[i]) <= 57:
            i += 1
            exp_digits += 1
        if exp_digits == 0:
            return False
    return i == end


def number(text: String, name: String) raises -> Float64:
    """Number: invariant-culture NumberStyles.Float, and finite. Otherwise a user error.
    """
    if is_number_text(text):
        var v = Float64(text.strip())
        if isfinite(v):
            return v
    raise user_error("<" + name + "> must be a number, got '" + text + "'")


def pad_left(s: String, width: Int) raises -> String:
    """C#'s {x,n} for a positive n: spaces before, never cut."""
    var out = String()
    for _ in range(width - utf16_length(s)):
        out += " "
    return out + s


def note_line(n: Note) raises -> String:
    """NoteLine: id, text, and the refs and tags in brackets when there are any.
    A source ref carries the place inside it, as s1:p.42."""
    var parts = List[String]()
    for r in n.refs:
        if r.startswith("s") and n.where_ is not None:
            parts.append(r + ":" + n.where_.value())
        else:
            parts.append(r)
    for t in n.tags:
        parts.append("+" + t)
    var line = pad_right(n.id, 4) + " " + n.text
    if len(parts) > 0:
        line += "  [" + " ".join(parts) + "]"
    return line


def prediction_line(h: Prediction) raises -> String:
    """PredictionLine: id, verdict (right, wrong or "open "), confidence, claim.
    """
    var verdict = String("open ")
    if h.right is not None:
        verdict = String("right") if h.right.value() else String("wrong")
    return (
        pad_right(h.id, 4)
        + " "
        + verdict
        + " "
        + pad_left(num(h.confidence), 3)
        + "%  "
        + h.text
    )


struct Session(Movable):
    """The journal, the state folded once at start, and the clock for one command.
    """

    var journal: JournalTx
    var state: State
    var now: Instant
    # The lines the window and git tracker wrote before this command (track.mojo).
    # `vinci sync` prints them; every other command ignores them.
    var tracked: List[String]

    def __init__(out self, var journal: JournalTx, var now: Instant) raises:
        """Reads the journal once, under the transaction's lock. A broken journal fails here,
        even for help, as in C#. The caller commits or aborts the transaction.
        """
        self.state = state_from(journal.read())
        self.journal = journal^
        self.now = now^
        self.tracked = List[String]()

    def record(mut self, e: Event) raises:
        """Queue one fact for the transaction. The state is not refolded; later reads in
        this command see the old one.
        """
        self.journal.append(e.copy())

    def commit(mut self) raises:
        """Writes the command's facts with one fsync and releases the lock."""
        self.journal.commit()

    def abort(mut self) raises:
        """Drops the command's facts and releases the lock, after an error."""
        self.journal.abort()

    def read_input(self) raises -> String:
        """Everything on standard input: what a lone "-" argument means."""
        var sys = Python.import_module("sys")
        return String(sys.stdin.read())

    def arg(self, a: List[String], index: Int, name: String) raises -> String:
        """Arg: the argument at index, whitespace collapsed. "-" reads standard input.
        Missing or empty is a user error that names <name>."""
        if index >= len(a):
            raise user_error("missing <" + name + ">")
        var value: String
        if a[index] == "-":
            value = self.read_input()
        else:
            value = a[index]
        var collapsed = collapse_spaces(value)
        if collapsed.byte_length() == 0:
            raise user_error("<" + name + "> is empty")
        return collapsed

    def idea_arg(self, a: List[String]) raises -> Idea:
        """IdeaArg: the first argument must name an idea. Returns a copy of it.
        """
        var id = self.arg(a, 0, "idea id")
        var i = self.state.idea_index(id)
        if i < 0:
            raise user_error("no idea " + id)
        return self.state.ideas[i].copy()

    def next_id(self, prefix: String) raises -> String:
        """NextId: the next id for a prefix letter, from the folded state (i1, r2, ...).
        """
        return self.state.next_id(prefix)

    def describe(self, id: String) raises -> String:
        """Describe: the note, prediction or idea line for an id."""
        var n = self.state.note_index(id)
        if n >= 0:
            return note_line(self.state.notes[n])
        var h = self.state.prediction_index(id)
        if h >= 0:
            return prediction_line(self.state.predictions[h])
        var i = self.state.idea_index(id)
        if i < 0:
            raise Error("describe: no idea, note or prediction " + id)
        return pad_right(id, 4) + " " + self.state.ideas[i].title

    def section(self, title: String, lines: List[String]):
        """Section: a title and indented lines, printed only when there are lines.
        """
        if len(lines) == 0:
            return
        print(title)
        for line in lines:
            print("  " + line)
