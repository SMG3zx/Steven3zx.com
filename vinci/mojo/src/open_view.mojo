# What `vinci open` prints for one stored text: the header, and part of its lines.
#
# Lines are 1-based. `lines=A-B` takes the lines A to B (B is clipped to the last line).
# `grep=word` takes each line that contains word, ignoring ASCII case, with one line of
# context on each side. Without either, the whole text is taken. Whatever is taken is
# capped at OPEN_CAP_BYTES, cut at a line boundary, and the notice says which request
# reads on. A single line longer than the cap is cut inside the line, and the notice
# says so: no option reads past that cut.

from handles import Stored, date_of
from jsonline import ascii_lower, json_string
from session import user_error
from text import ascii_tail
from timeutil import ascii_prefix

comptime OPEN_CAP_BYTES = 4000


@fieldwise_init
struct Request(Copyable, Movable):
    var has_lines: Bool
    var first: Int
    var last: Int
    var has_grep: Bool
    var word: String


@fieldwise_init
struct OpenLine(Copyable, Movable):
    var n: Int
    var text: String


@fieldwise_init
struct View(Copyable, Movable):
    var id: String
    var kind: String
    var at: String
    var note: String
    var total: Int
    var lines: List[OpenLine]
    var truncated: Bool
    var more: String
    var notice: String


def no_request() -> Request:
    return Request(False, 0, 0, False, String())


def parse_lines_arg(value: String, mut req: Request) raises:
    """lines=A-B: sets req's range. Errors name the form, so a typo is easy to fix.
    """
    var dash = value.find("-")
    if dash < 1 or dash == value.byte_length() - 1:
        raise user_error("lines= needs A-B, for example lines=10-20")
    var first: Int
    var last: Int
    try:
        first = Int(String(ascii_prefix(value, dash)))
        last = Int(String(ascii_tail(value, dash + 1)))
    except:
        raise user_error("lines= needs whole numbers, for example lines=10-20")
    if first < 1 or last < first:
        raise user_error(
            "lines=" + value + " needs 1 <= A <= B (lines are numbered from 1)"
        )
    req.has_lines = True
    req.first = first
    req.last = last


def split_lines(text: String) raises -> List[String]:
    """The lines of text. A final newline ends the last line; it does not add one.
    """
    var out = List[String]()
    if text.byte_length() == 0:
        return out^
    for raw in text.split("\n"):
        out.append(String(raw))
    if len(out) > 0 and out[len(out) - 1].byte_length() == 0:
        _ = out.pop()
    return out^


def utf8_prefix(s: String, count: Int) -> String:
    """The first count bytes of s, moved back to a character boundary."""
    var b = s.as_bytes()
    if count >= len(b):
        return s.copy()
    var end = count
    while end > 0 and (Int(b[end]) & 0xC0) == 0x80:
        end -= 1
    var out = List[UInt8]()
    for i in range(end):
        out.append(b[i])
    return String(unsafe_from_utf8=out)


def contains_word(line: String, word: String) -> Bool:
    return ascii_lower(line).find(ascii_lower(word)) >= 0


def build_view(id: String, item: Stored, req: Request, cap: Int) raises -> View:
    """The view of one stored text under a request. Raises when a lines= range starts
    past the end of the text."""
    var lines = split_lines(item.text)
    var total = len(lines)
    var selected = List[Int]()
    var range_last = total
    if req.has_lines:
        if req.first > total:
            raise user_error(
                "lines="
                + String(req.first)
                + "-"
                + String(req.last)
                + " are past the end: "
                + id
                + " has "
                + String(total)
                + " lines"
            )
        if req.last < range_last:
            range_last = req.last
        for n in range(req.first, range_last + 1):
            selected.append(n)
    else:
        for n in range(1, total + 1):
            selected.append(n)

    var notice = String()
    if req.has_grep:
        # A line is kept when it matches, or it is next to a match. The range, if any,
        # still limits the result.
        var marked = List[Bool]()
        for _ in range(total + 2):
            marked.append(False)
        var any_match = False
        for n in range(1, total + 1):
            if contains_word(lines[n - 1], req.word):
                any_match = True
                marked[n] = True
        if not any_match:
            notice = "no line matches '" + req.word + "'"
        var kept = List[Int]()
        for n in selected:
            var near = (
                marked[n] or marked[n - 1] or (n + 1 <= total and marked[n + 1])
            )
            if near:
                kept.append(n)
        selected = kept^

    var out = List[OpenLine]()
    var used = 0
    var truncated = False
    var cut_inside = False
    var next_n = 0
    for k in range(len(selected)):
        var n = selected[k]
        var line = lines[n - 1].copy()
        var cost = line.byte_length() + 1
        if used + cost > cap:
            truncated = True
            next_n = n
            if len(out) == 0:
                # One line over the cap: show its start, and say that it is cut.
                out.append(OpenLine(n, utf8_prefix(line, cap)))
                cut_inside = True
                next_n = n + 1
            break
        out.append(OpenLine(n, line^))
        used += cost

    var more = String()
    if truncated and not req.has_grep:
        var stop = total
        if req.has_lines:
            stop = range_last
        if next_n <= stop:
            more = "lines=" + String(next_n) + "-" + String(stop)
    if cut_inside:
        notice = (
            "line "
            + String(out[0].n)
            + " is cut at "
            + String(cap)
            + " bytes; no option reads past that cut"
        )
        if more.byte_length() > 0:
            notice += "; open " + id + " " + more + " for the lines after it"
    elif truncated and req.has_grep:
        notice = "more matches are cut: narrow the word, or read lines=A-B"
    elif truncated:
        var shown_from = 1
        if req.has_lines:
            shown_from = req.first
        notice = (
            "showing lines "
            + String(shown_from)
            + "-"
            + String(next_n - 1)
            + " of "
            + String(total)
            + "; open "
            + id
            + " "
            + more
            + " for more"
        )
    return View(
        id,
        item.kind.copy(),
        date_of(item.at),
        item.note.copy(),
        total,
        out^,
        truncated,
        more,
        notice,
    )


def header_of(v: View) -> String:
    var out = (
        "["
        + v.id
        + "] "
        + v.kind
        + " "
        + v.at
        + " ("
        + String(v.total)
        + " lines)"
    )
    if v.note.byte_length() > 0:
        out += " [" + v.note + "]"
    return out


def render_text(v: View) -> String:
    """The header, then each line as `n: text`. A gap in the numbers prints as `--`.
    """
    var out = header_of(v)
    var prev = 0
    for line in v.lines:
        if prev > 0 and line.n != prev + 1:
            out += "\n--"
        out += "\n" + String(line.n) + ": " + line.text
        prev = line.n
    if v.notice.byte_length() > 0:
        out += "\n" + v.notice
    return out


def render_json(v: View) raises -> String:
    var out = (
        '{"id":'
        + json_string(v.id)
        + ',"kind":'
        + json_string(v.kind)
        + ',"date":'
        + json_string(v.at)
        + ',"note":'
        + json_string(v.note)
        + ',"total_lines":'
        + String(v.total)
        + ',"lines":['
    )
    for i in range(len(v.lines)):
        if i > 0:
            out += ","
        out += (
            '{"n":'
            + String(v.lines[i].n)
            + ',"text":'
            + json_string(v.lines[i].text)
            + "}"
        )
    out += '],"truncated":' + ("true" if v.truncated else "false")
    out += ',"more":'
    if v.more.byte_length() > 0:
        out += json_string(v.more)
    else:
        out += "null"
    return out + ',"notice":' + json_string(v.notice) + "}"
