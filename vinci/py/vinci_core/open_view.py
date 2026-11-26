"""What `vinci open` prints for one stored text: the header, and part of its lines.

Lines are 1-based. `lines=A-B` takes the lines A to B (B is clipped to the last line). `grep=word`
takes each line that contains word, ignoring ASCII case, with one line of context on each side.
Without either, the whole text is taken. Whatever is taken is capped at OPEN_CAP_BYTES, cut at
a line boundary, and the notice says which request reads on. A single line longer than the cap
is cut inside the line, and the notice says so: no option reads past that cut.

Port of mojo/src/open_view.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass

from .handles import Stored, date_of
from .jsonline import ascii_lower, json_string
from .session import user_error

OPEN_CAP_BYTES = 4000


@dataclass
class Request:
    has_lines: bool = False
    first: int = 0
    last: int = 0
    has_grep: bool = False
    word: str = ""


@dataclass
class OpenLine:
    n: int
    text: str


@dataclass
class View:
    id: str
    kind: str
    at: str
    note: str
    total: int
    lines: list[OpenLine]
    truncated: bool
    more: str
    notice: str


def parse_lines_arg(value: str, req: Request) -> None:
    """Lines=A-B: sets req's range. Errors name the form, so a typo is easy to fix."""
    dash = value.find("-")
    if dash < 1 or dash == len(value) - 1:
        raise user_error("lines= needs A-B, for example lines=10-20")
    try:
        first = int(value[:dash])
        last = int(value[dash + 1 :])
    except ValueError:
        raise user_error("lines= needs whole numbers, for example lines=10-20") from None
    if first < 1 or last < first:
        raise user_error("lines=" + value + " needs 1 <= A <= B (lines are numbered from 1)")
    req.has_lines = True
    req.first = first
    req.last = last


def split_lines(text: str) -> list[str]:
    """The lines of text. A final newline ends the last line; it does not add one."""
    if len(text) == 0:
        return []
    out = text.split("\n")
    if len(out) > 0 and out[-1] == "":
        out.pop()
    return out


def utf8_prefix(s: bytes, count: int) -> bytes:
    """The first count bytes of s, moved back to a character boundary."""
    if count >= len(s):
        return s
    end = count
    while end > 0 and (s[end] & 0xC0) == 0x80:
        end -= 1
    return s[:end]


def contains_word(line: str, word: str) -> bool:
    return ascii_lower(word) in ascii_lower(line)


def build_view(ident: str, item: Stored, req: Request, cap: int) -> View:
    """The view of one stored text under a request. Raises when a lines= range starts past the
    end of the text."""
    lines = split_lines(item.text)
    total = len(lines)
    range_last = total
    if req.has_lines:
        if req.first > total:
            raise user_error(
                "lines=" + str(req.first) + "-" + str(req.last) + " are past the end: "
                + ident + " has " + str(total) + " lines"
            )
        if req.last < range_last:
            range_last = req.last
        selected = list(range(req.first, range_last + 1))
    else:
        selected = list(range(1, total + 1))

    notice = ""
    if req.has_grep:
        # A line is kept when it matches, or it is next to a match. The range, if any, still
        # limits the result.
        marked = [False] * (total + 2)
        any_match = False
        for n in range(1, total + 1):
            if contains_word(lines[n - 1], req.word):
                any_match = True
                marked[n] = True
        if not any_match:
            notice = "no line matches '" + req.word + "'"
        kept: list[int] = []
        for n in selected:
            near = marked[n] or marked[n - 1] or (n + 1 <= total and marked[n + 1])
            if near:
                kept.append(n)
        selected = kept

    out: list[OpenLine] = []
    used = 0
    truncated = False
    cut_inside = False
    next_n = 0
    for n in selected:
        line = lines[n - 1]
        raw = line.encode("utf-8")
        cost = len(raw) + 1
        if used + cost > cap:
            truncated = True
            next_n = n
            if len(out) == 0:
                # One line over the cap: show its start, and say that it is cut.
                out.append(OpenLine(n, utf8_prefix(raw, cap).decode("utf-8", errors="ignore")))
                cut_inside = True
                next_n = n + 1
            break
        out.append(OpenLine(n, line))
        used += cost

    more = ""
    if truncated and not req.has_grep:
        stop = range_last if req.has_lines else total
        if next_n <= stop:
            more = "lines=" + str(next_n) + "-" + str(stop)
    if cut_inside:
        notice = (
            "line " + str(out[0].n) + " is cut at " + str(cap)
            + " bytes; no option reads past that cut"
        )
        if len(more) > 0:
            notice += "; open " + ident + " " + more + " for the lines after it"
    elif truncated and req.has_grep:
        notice = "more matches are cut: narrow the word, or read lines=A-B"
    elif truncated:
        shown_from = req.first if req.has_lines else 1
        notice = (
            "showing lines " + str(shown_from) + "-" + str(next_n - 1) + " of " + str(total)
            + "; open " + ident + " " + more + " for more"
        )
    return View(ident, item.kind, date_of(item.at), item.note, total, out, truncated, more, notice)


def header_of(v: View) -> str:
    out = "[" + v.id + "] " + v.kind + " " + v.at + " (" + str(v.total) + " lines)"
    if len(v.note) > 0:
        out += " [" + v.note + "]"
    return out


def render_text(v: View) -> str:
    """The header, then each line as `n: text`. A gap in the numbers prints as `--`."""
    out = header_of(v)
    prev = 0
    for line in v.lines:
        if prev > 0 and line.n != prev + 1:
            out += "\n--"
        out += "\n" + str(line.n) + ": " + line.text
        prev = line.n
    if len(v.notice) > 0:
        out += "\n" + v.notice
    return out


def render_json(v: View) -> str:
    out = (
        '{"id":' + json_string(v.id)
        + ',"kind":' + json_string(v.kind)
        + ',"date":' + json_string(v.at)
        + ',"note":' + json_string(v.note)
        + ',"total_lines":' + str(v.total)
        + ',"lines":['
    )
    out += ",".join('{"n":' + str(line.n) + ',"text":' + json_string(line.text) + "}" for line in v.lines)
    out += '],"truncated":' + ("true" if v.truncated else "false")
    out += ',"more":' + (json_string(v.more) if len(v.more) > 0 else "null")
    return out + ',"notice":' + json_string(v.notice) + "}"
