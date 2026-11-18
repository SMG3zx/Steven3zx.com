# HTML text for the portal page: WebUtility.HtmlEncode and HtmlDecode as .NET 10 has them, and
# the title Portal.TitleOf reads from a page. Output goes into a byte buffer, so a page is
# built in one pass.
#
# The entity names come from portal_entities.mojo (generated from .NET). Numeric references
# follow the grammar checked against .NET: decimal allows white space and a sign around the
# digits, hex allows neither, and a reference must end with a semicolon and name a code
# point that is not a surrogate and not above U+10FFFF. Anything else stays as typed.

from portal_entities import entity_code_point
from session import is_space
from text import code_points_to_string, utf8_append, utf8_code_points


def put(mut out: List[UInt8], text: String):
    for b in text.as_bytes():
        out.append(b)


def put_code_point(mut out: List[UInt8], cp: Int):
    utf8_append(out, cp)


def html_encode_into(mut out: List[UInt8], text: String) raises:
    """WebUtility.HtmlEncode: & < > " and ' are escaped, so are code points 0xA0 to 0xFF and
    everything from U+10000 up; the rest is copied."""
    for cp in utf8_code_points(text):
        if cp == 38:
            put(out, "&amp;")
        elif cp == 60:
            put(out, "&lt;")
        elif cp == 62:
            put(out, "&gt;")
        elif cp == 34:
            put(out, "&quot;")
        elif cp == 39:
            put(out, "&#39;")
        elif (cp >= 0xA0 and cp <= 0xFF) or cp >= 0x10000:
            put(out, "&#" + String(cp) + ";")
        else:
            put_code_point(out, cp)


def html_encode(text: String) raises -> String:
    var out = List[UInt8]()
    html_encode_into(out, text)
    return String(unsafe_from_utf8=out)


@fieldwise_init
struct NumericRef(Copyable, Movable):
    """A numeric reference read at some index: ok, its code point, and the index after it.
    """

    var ok: Bool
    var code: Int
    var end: Int


def is_white(c: Int) -> Bool:
    return c == 32 or (c >= 9 and c <= 13)


def hex_value_of(c: Int) -> Int:
    """The value of a hex digit, or -1."""
    if c >= 48 and c <= 57:
        return c - 48
    if c >= 65 and c <= 70:
        return c - 55
    if c >= 97 and c <= 102:
        return c - 87
    return -1


def valid_code_point(code: Int) -> Bool:
    return (
        code >= 0
        and code <= 0x10FFFF
        and not (code >= 0xD800 and code <= 0xDFFF)
    )


def numeric_ref(cps: List[Int], start: Int) -> NumericRef:
    """The reference whose digits begin at start (just after "&#"). Not ok when it does
    not match the grammar."""
    var n = len(cps)
    var fail = NumericRef(False, 0, start)
    if start < n and (cps[start] == 120 or cps[start] == 88):
        var q = start + 1
        var value = 0
        var digits = 0
        var too_big = False
        while q < n and hex_value_of(cps[q]) >= 0:
            value = value * 16 + hex_value_of(cps[q])
            if value > 0x10FFFF:
                too_big = True
                value = 0x110000
            digits += 1
            q += 1
        if digits == 0 or q >= n or cps[q] != 59 or too_big:
            return fail^
        if not valid_code_point(value):
            return fail^
        return NumericRef(True, value, q + 1)
    var q = start
    while q < n and is_white(cps[q]):
        q += 1
    var negative = False
    if q < n and (cps[q] == 43 or cps[q] == 45):
        negative = cps[q] == 45
        q += 1
    var digits = 0
    var value = 0
    var too_big = False
    while q < n and cps[q] >= 48 and cps[q] <= 57:
        value = value * 10 + (cps[q] - 48)
        if value > 0x10FFFF:
            too_big = True
            value = 0x110000
        digits += 1
        q += 1
    if digits == 0:
        return fail^
    while q < n and is_white(cps[q]):
        q += 1
    if q >= n or cps[q] != 59:
        return fail^
    if too_big:
        return fail^
    if negative:
        if value != 0:
            return fail^
    if not valid_code_point(value):
        return fail^
    return NumericRef(True, value, q + 1)


def html_decode(text: String) raises -> String:
    """WebUtility.HtmlDecode: named references from the .NET table, numeric references by the
    grammar above. A reference that does not match stays as typed."""
    var cps = utf8_code_points(text)
    var n = len(cps)
    var out = List[UInt8]()
    var i = 0
    while i < n:
        var c = cps[i]
        if c != 38:
            put_code_point(out, c)
            i += 1
            continue
        if i + 1 < n and cps[i + 1] == 35:
            var numeric = numeric_ref(cps, i + 2)
            if numeric.ok:
                put_code_point(out, numeric.code)
                i = numeric.end
                continue
            put_code_point(out, c)
            i += 1
            continue
        var q = i + 1
        while q < n and (
            (cps[q] >= 48 and cps[q] <= 57)
            or (cps[q] >= 65 and cps[q] <= 90)
            or (cps[q] >= 97 and cps[q] <= 122)
        ):
            q += 1
        if q > i + 1 and q < n and cps[q] == 59:
            var code = entity_code_point(
                code_points_to_string(_slice(cps, i + 1, q))
            )
            if code >= 0:
                put_code_point(out, code)
                i = q + 1
                continue
        put_code_point(out, c)
        i += 1
    return String(unsafe_from_utf8=out)


def _slice(cps: List[Int], start: Int, end: Int) -> List[Int]:
    var out = List[Int]()
    for i in range(start, end):
        out.append(cps[i])
    return out^


def ascii_lower_byte(c: Int) -> Int:
    if c >= 65 and c <= 90:
        return c + 32
    return c


def matches_ignore_case(b: List[UInt8], at: Int, word: String) -> Bool:
    """True when the bytes at `at` spell word, ignoring ASCII case."""
    var w = word.as_bytes()
    if at + len(w) > len(b):
        return False
    for k in range(len(w)):
        if ascii_lower_byte(Int(b[at + k])) != ascii_lower_byte(Int(w[k])):
            return False
    return True


def title_of(html: String) raises -> Optional[String]:
    """Portal.TitleOf: the first <title ...>...</title> with no tag inside it, its entities
    decoded and its white space collapsed. None when there is no such title or it is empty.
    """
    var b = List[UInt8]()
    for byte in html.as_bytes():
        b.append(byte)
    var n = len(b)
    for start in range(n):
        if not matches_ignore_case(b, start, "<title"):
            continue
        var close = -1
        for k in range(start + 6, n):
            if b[k] == 62:
                close = k
                break
        if close < 0:
            continue
        var lt = -1
        for k in range(close + 1, n):
            if b[k] == 60:
                lt = k
                break
        if lt < 0:
            continue
        if not matches_ignore_case(b, lt, "</title>"):
            continue
        var raw = List[UInt8]()
        for k in range(close + 1, lt):
            raw.append(b[k])
        var decoded = utf8_code_points(
            html_decode(String(unsafe_from_utf8=raw))
        )
        var collapsed = List[Int]()
        var pending = False
        for cp in decoded:
            if is_space(cp):
                pending = True
            else:
                if pending and len(collapsed) > 0:
                    collapsed.append(32)
                pending = False
                collapsed.append(cp)
        var title = code_points_to_string(collapsed)
        if title.byte_length() == 0:
            return None
        return Optional(title)
    return None
