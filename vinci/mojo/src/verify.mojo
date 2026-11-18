# `vinci verify [--index] [--strict]` (K0b): a read-only check of journal.jsonl.
#
# Errors make it exit 1: the file does not end with a newline; a line is not a JSON object that
# parse_event reads (so no at, no offset, not an event); a line has no kind; or an id that must
# be unique repeats. Warnings do not fail unless --strict: an unknown kind (a newer build's, or a
# typo), and a line whose at is more than a day after now. A line out of time order is a note only.
#
# The ids that must be unique are the ones the journal mints (state.mojo): idea, req, note,
# source, issue, principle, predict, bookmark, read and exchange. Versioned kinds (for, goal,
# home, remember, memory and the rest) and the lines that repeat an id on purpose (work, spent,
# used) are not checked for repeats, as memory.md's versioning rules say.
#
# --index rebuilds the journal's facts into a temporary folder and compares the current journal
# facts with the stored index.sqlite (verify_edge.py). A difference is a warning. The stored index
# is opened read-only and the journal is never written.

from std.pathlib import Path
from std.python import Python, PythonObject
from std.sys import exit

from embed_edge import edge_module
from index_build import update_from_journal
from index_store import IndexStore
from journal import Event, parse_event
from session import user_error
from text import ascii_tail
from timeutil import (
    TICKS_PER_DAY,
    Instant,
    ascii_prefix,
    format_instant,
    parse_instant,
)

comptime FUTURE_TICKS = TICKS_PER_DAY
comptime SHOW_LIMIT = 20

# Every kind state.mojo's fold knows, plus tend (which changes nothing but when an idea was
# touched). A kind outside this list is a warning, so an older build can still read a newer
# journal, as state.mojo does.
comptime KNOWN_KINDS = (
    " idea for goal req cut stage watch unwatch bookmark unbookmark home work"
    " sync spent ship used retire source note link log spec issue diagnose"
    " principle predict result commit kept missed review remember memory read"
    " exchange tend "
)
# The kinds whose id is minted once, so a second line with the same id is an error.
comptime UNIQUE_KINDS = (
    " idea req note source issue principle predict bookmark read exchange "
)


@fieldwise_init
struct KindTotal(Copyable, Movable):
    var kind: String
    var total: Int


struct Finding(Copyable, Movable):
    """One problem, with its line number (0 when it is about the file)."""

    var line: Int
    var text: String

    def __init__(out self, line: Int, var text: String):
        self.line = line
        self.text = text^


struct VerifyReport(Movable):
    """The result of one pass over the journal."""

    var lines: Int
    var kinds: List[KindTotal]
    var errors: List[Finding]
    var warnings: List[Finding]
    var notes: Int
    var first: Optional[String]
    var last: Optional[String]

    def __init__(out self):
        self.lines = 0
        self.kinds = List[KindTotal]()
        self.errors = List[Finding]()
        self.warnings = List[Finding]()
        self.notes = 0
        self.first = None
        self.last = None

    def count_kind(mut self, kind: String):
        for i in range(len(self.kinds)):
            if self.kinds[i].kind == kind:
                self.kinds[i].total += 1
                return
        self.kinds.append(KindTotal(kind, 1))


def in_list(list_text: String, word: String) -> Bool:
    """True when word is one of the space-separated words of list_text."""
    return (" " + word + " ") in list_text


def check_line(
    text: String,
    number: Int,
    mut seen: List[String],
    mut report: VerifyReport,
    now_ticks: Int,
    mut previous_ticks: Int,
) raises:
    """Checks one complete line and adds to the report. seen gets the unique ids of this run.
    """
    if text.byte_length() == 0:
        report.errors.append(Finding(number, "a blank line"))
        return
    var e: Event
    try:
        e = parse_event(text)
    except err:
        report.errors.append(
            Finding(number, "not a journal event (" + String(err) + ")")
        )
        return
    report.lines += 1
    if e.kind.byte_length() == 0:
        report.errors.append(Finding(number, "no kind"))
        return
    report.count_kind(e.kind)
    var ticks = e.at.utc
    if report.first is None:
        report.first = Optional[String](format_instant(e.at.copy()))
    report.last = Optional[String](format_instant(e.at.copy()))
    if previous_ticks > 0 and ticks < previous_ticks:
        report.notes += 1
    previous_ticks = ticks
    if ticks > now_ticks + FUTURE_TICKS:
        report.warnings.append(
            Finding(
                number,
                "the time "
                + format_instant(e.at.copy())
                + " is more than a day after now",
            )
        )
    if not in_list(KNOWN_KINDS, e.kind):
        report.warnings.append(
            Finding(number, "an unknown kind '" + e.kind + "'")
        )
    if in_list(UNIQUE_KINDS, e.kind) and e.id is not None:
        var id = e.kind + ":" + e.id.value()
        for s in seen:
            if s == id:
                report.errors.append(
                    Finding(
                        number,
                        "the id "
                        + e.id.value()
                        + " is minted twice ("
                        + e.kind
                        + ")",
                    )
                )
                return
        seen.append(id)


def verify_text(text: String, now_ticks: Int) raises -> VerifyReport:
    """The report for the journal's text: the lines, the kinds, and what is wrong with them.
    """
    var report = VerifyReport()
    if text.byte_length() > 0 and text.as_bytes()[text.byte_length() - 1] != 10:
        report.errors.append(
            Finding(
                0, "the file does not end with a newline (a torn last line)"
            )
        )
    var seen = List[String]()
    var previous = 0
    var number = 0
    var pieces = text.split("\n")
    for raw in pieces:
        number += 1
        var line = String(raw)
        if number == len(pieces) and line.byte_length() == 0:
            break
        if (
            line.byte_length() > 0
            and line.as_bytes()[line.byte_length() - 1] == 13
        ):
            line = ascii_prefix(line, line.byte_length() - 1)
        check_line(line, number, seen, report, now_ticks, previous)
    return report^


def flag_word(args: List[String], word: String) -> Bool:
    for a in args:
        if a == word:
            return True
    return False


def print_findings(label: String, items: List[Finding]):
    var shown = 0
    for item in items:
        if shown == SHOW_LIMIT:
            print("  ... " + String(len(items) - SHOW_LIMIT) + " more " + label)
            break
        if item.line > 0:
            print(
                "  " + label + " line " + String(item.line) + ": " + item.text
            )
        else:
            print("  " + label + ": " + item.text)
        shown += 1


def index_check(
    journal: String, home: String
) raises -> Tuple[String, String, Int]:
    """Rebuilds the journal's facts in a temporary folder and compares them with the stored
    index. Returns (status, text, differences)."""
    _ = edge_module()
    var edge = Python.import_module("vinci_edge.verify_edge")
    var scratch = edge.scratch_folder()
    if String(scratch[0]) != "ok":
        return ("error", String(scratch[1]), 0)
    var folder = String(scratch[1])
    var status = String("error")
    var text = String()
    var differences = 0
    try:
        var store = IndexStore(folder + "/index.sqlite")
        _ = update_from_journal(store, journal, home)
        var stored = home + "/index.sqlite"
        var pair = edge.compare(
            PythonObject(stored), PythonObject(folder + "/index.sqlite")
        )
        status = String(pair[0])
        text = String(pair[1])
        differences = Int(String(pair[2]))
    except err:
        text = "cannot rebuild the index: " + String(err)
    edge.remove_folder(PythonObject(folder))
    return (status, text, differences)


def verify_command(
    args: List[String], home: String, journal: String, now: Instant
) raises:
    """`vinci verify`: prints the summary and the findings. Exits 1 on an error (and on a
    warning with --strict), 0 otherwise. A bad flag is a user error."""
    var index = False
    var strict = False
    for a in args:
        if a == "--index":
            index = True
        elif a == "--strict":
            strict = True
        else:
            raise user_error(
                "verify takes --index and --strict, got '" + a + "'"
            )
    if not Path(journal).exists():
        raise user_error("there is no journal at " + journal)
    # A reader: no lock, and nothing created. A line a writer is still writing has no
    # newline yet, and verify reports it as the torn last line it would be.
    var text = Path(journal).read_text()

    var report = verify_text(text, now.utc)
    print("verify: " + journal)
    print("  lines " + String(report.lines))
    var kinds = String()
    for k in report.kinds:
        if kinds.byte_length() > 0:
            kinds += ", "
        kinds += k.kind + " " + String(k.total)
    print("  kinds " + kinds)
    if report.first is not None:
        print("  first " + report.first.value())
        print("  last  " + report.last.value())
    print_findings("error", report.errors)
    print_findings("warning", report.warnings)
    var errors = len(report.errors)
    var warnings = len(report.warnings)
    if index:
        var check = index_check(journal, home)
        print("  " + check[1].replace("\n", "\n  "))
        if check[0] == "error":
            print("  error: the index check did not run")
            errors += 1
        elif check[0] != "same":
            print(
                "  warning: the stored index differs from a rebuild of the"
                " journal"
            )
            warnings += 1
    print("  errors " + String(errors))
    print("  warnings " + String(warnings))
    print(
        "  notes "
        + String(report.notes)
        + " (lines out of time order; the journal is append-only)"
    )
    if errors > 0 or (strict and warnings > 0):
        print("verify: failed")
        exit(1)
    print("verify: ok")
