"""`vinci verify [--index] [--strict]` (K0b): a read-only check of journal.jsonl.

Errors make it exit 1: the file does not end with a newline; a line is not a JSON object that
parse_event reads (so no at, no offset, not an event); a line has no kind; or an id that must be
unique repeats. Warnings do not fail unless --strict: an unknown kind (a newer build's, or a
typo), and a line whose at is more than a day after now. A line out of time order is a note only.

--index rebuilds the journal's facts into a temporary folder and compares the current journal
facts with the stored index.sqlite (vinci_edge.verify_edge). A difference is a warning. The
stored index is opened read-only and the journal is never written.

Port of mojo/src/verify.mojo. verify_command returns the exit code.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional

from vinci_edge import verify_edge

from .index_build import update_from_journal
from .index_store import IndexStore
from .journal import Event, parse_event
from .session import user_error
from .timeutil import TICKS_PER_DAY, Instant, format_instant

FUTURE_TICKS = TICKS_PER_DAY
SHOW_LIMIT = 20

# Every kind state's fold knows, plus tend (which changes nothing but when an idea was touched).
KNOWN_KINDS = frozenset(
    "idea for goal req cut stage watch unwatch bookmark unbookmark home work sync spent ship used "
    "retire source note link log spec issue diagnose principle predict result commit kept missed "
    "review remember memory read exchange tend".split(" ")
)
# The kinds whose id is minted once, so a second line with the same id is an error.
UNIQUE_KINDS = frozenset("idea req note source issue principle predict bookmark read exchange".split(" "))


@dataclass
class Finding:
    """One problem, with its line number (0 when it is about the file)."""

    line: int
    text: str


@dataclass
class VerifyReport:
    """The result of one pass over the journal."""

    lines: int = 0
    kinds: dict[str, int] = field(default_factory=dict)
    errors: list[Finding] = field(default_factory=list)
    warnings: list[Finding] = field(default_factory=list)
    notes: int = 0
    first: Optional[str] = None
    last: Optional[str] = None

    def count_kind(self, kind: str) -> None:
        self.kinds[kind] = self.kinds.get(kind, 0) + 1


def check_line(text: str, number: int, seen: list[str], report: VerifyReport, now_ticks: int, previous: list[int]) -> None:
    """Checks one complete line and adds to the report. seen gets the unique ids of this run."""
    if len(text) == 0:
        report.errors.append(Finding(number, "a blank line"))
        return
    try:
        e: Event = parse_event(text)
    except ValueError as err:
        report.errors.append(Finding(number, "not a journal event (" + str(err) + ")"))
        return
    report.lines += 1
    if e.kind == "":
        report.errors.append(Finding(number, "no kind"))
        return
    report.count_kind(e.kind)
    ticks = e.at.utc
    if report.first is None:
        report.first = format_instant(e.at)
    report.last = format_instant(e.at)
    if previous[0] > 0 and ticks < previous[0]:
        report.notes += 1
    previous[0] = ticks
    if ticks > now_ticks + FUTURE_TICKS:
        report.warnings.append(
            Finding(number, "the time " + format_instant(e.at) + " is more than a day after now")
        )
    if e.kind not in KNOWN_KINDS:
        report.warnings.append(Finding(number, "an unknown kind '" + e.kind + "'"))
    if e.kind in UNIQUE_KINDS and e.id is not None:
        ident = e.kind + ":" + e.id
        if ident in seen:
            report.errors.append(Finding(number, "the id " + e.id + " is minted twice (" + e.kind + ")"))
            return
        seen.append(ident)


def verify_text(text: str, now_ticks: int) -> VerifyReport:
    """The report for the journal's text: the lines, the kinds, and what is wrong with them."""
    report = VerifyReport()
    if len(text) > 0 and not text.endswith("\n"):
        report.errors.append(Finding(0, "the file does not end with a newline (a torn last line)"))
    seen: list[str] = []
    previous = [0]
    pieces = text.split("\n")
    for number, raw in enumerate(pieces, start=1):
        line = raw
        if number == len(pieces) and len(line) == 0:
            break
        if line.endswith("\r"):
            line = line[:-1]
        check_line(line, number, seen, report, now_ticks, previous)
    return report


def print_findings(label: str, items: list[Finding]) -> None:
    shown = 0
    for item in items:
        if shown == SHOW_LIMIT:
            print("  ... " + str(len(items) - SHOW_LIMIT) + " more " + label)
            break
        if item.line > 0:
            print("  " + label + " line " + str(item.line) + ": " + item.text)
        else:
            print("  " + label + ": " + item.text)
        shown += 1


def index_check(journal: str, home: str) -> tuple[str, str, int]:
    """Rebuilds the journal's facts in a temporary folder and compares them with the stored
    index. Returns (status, text, differences)."""
    status_scratch, folder = verify_edge.scratch_folder()
    if status_scratch != "ok":
        return ("error", folder, 0)
    status = "error"
    text = ""
    differences = 0
    try:
        store = IndexStore(folder + "/index.sqlite")
        update_from_journal(store, journal, home)
        stored = home + "/index.sqlite"
        status, text, differences = verify_edge.compare(stored, folder + "/index.sqlite")
    except Exception as err:
        text = "cannot rebuild the index: " + str(err)
    verify_edge.remove_folder(folder)
    return (status, text, differences)


def verify_command(args: list[str], home: str, journal: str, now: Instant) -> int:
    """Prints the summary and the findings. Returns 1 on an error (and on a warning with
    --strict), 0 otherwise. A bad flag is a user error."""
    index = False
    strict = False
    for a in args:
        if a == "--index":
            index = True
        elif a == "--strict":
            strict = True
        else:
            raise user_error("verify takes --index and --strict, got '" + a + "'")
    if not Path(journal).exists():
        raise user_error("there is no journal at " + journal)
    # A reader: no lock, and nothing created. A line a writer is still writing has no newline
    # yet, and verify reports it as the torn last line it would be.
    text = Path(journal).read_text(encoding="utf-8")

    report = verify_text(text, now.utc)
    print("verify: " + journal)
    print("  lines " + str(report.lines))
    kinds = ", ".join(k + " " + str(n) for k, n in report.kinds.items())
    print("  kinds " + kinds)
    if report.first is not None:
        print("  first " + report.first)
        print("  last  " + str(report.last))
    print_findings("error", report.errors)
    print_findings("warning", report.warnings)
    errors = len(report.errors)
    warnings = len(report.warnings)
    if index:
        check = index_check(journal, home)
        print("  " + check[1].replace("\n", "\n  "))
        if check[0] == "error":
            print("  error: the index check did not run")
            errors += 1
        elif check[0] != "same":
            print("  warning: the stored index differs from a rebuild of the journal")
            warnings += 1
    print("  errors " + str(errors))
    print("  warnings " + str(warnings))
    print("  notes " + str(report.notes) + " (lines out of time order; the journal is append-only)")
    if errors > 0 or (strict and warnings > 0):
        print("verify: failed")
        return 1
    print("verify: ok")
    return 0
