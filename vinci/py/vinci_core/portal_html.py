"""HTML text for the portal page: encoding and decoding of entities, and the title read from a
page. Numeric references follow this grammar: decimal allows white space and a sign around the
digits, hex allows neither, and a reference must end with a semicolon and name a code point that
is not a surrogate and not above U+10FFFF. Anything else stays as typed.

Port of mojo/src/portal_html.mojo.
"""

from __future__ import annotations

from typing import Optional

from .portal_entities import entity_code_point
from .session import is_space


def html_encode(text: str) -> str:
    """WebUtility.HtmlEncode: & < > " and ' are escaped, so are code points 0xA0 to 0xFF and
    everything from U+10000 up; the rest is copied."""
    out: list[str] = []
    for ch in text:
        cp = ord(ch)
        if cp == 38:
            out.append("&amp;")
        elif cp == 60:
            out.append("&lt;")
        elif cp == 62:
            out.append("&gt;")
        elif cp == 34:
            out.append("&quot;")
        elif cp == 39:
            out.append("&#39;")
        elif (0xA0 <= cp <= 0xFF) or cp >= 0x10000:
            out.append("&#" + str(cp) + ";")
        else:
            out.append(ch)
    return "".join(out)


def is_white(c: int) -> bool:
    return c == 32 or (9 <= c <= 13)


def hex_value_of(c: int) -> int:
    """The value of a hex digit, or -1."""
    if 48 <= c <= 57:
        return c - 48
    if 65 <= c <= 70:
        return c - 55
    if 97 <= c <= 102:
        return c - 87
    return -1


def valid_code_point(code: int) -> bool:
    return 0 <= code <= 0x10FFFF and not (0xD800 <= code <= 0xDFFF)


def numeric_ref(cps: list[int], start: int) -> Optional[tuple[int, int]]:
    """The reference whose digits begin at start (just after "&#"): (code point, index after
    it), or None when it does not match the grammar."""
    n = len(cps)
    if start < n and cps[start] in (120, 88):
        q = start + 1
        value = 0
        digits = 0
        too_big = False
        while q < n and hex_value_of(cps[q]) >= 0:
            value = value * 16 + hex_value_of(cps[q])
            if value > 0x10FFFF:
                too_big = True
                value = 0x110000
            digits += 1
            q += 1
        if digits == 0 or q >= n or cps[q] != 59 or too_big:
            return None
        if not valid_code_point(value):
            return None
        return (value, q + 1)
    q = start
    while q < n and is_white(cps[q]):
        q += 1
    negative = False
    if q < n and cps[q] in (43, 45):
        negative = cps[q] == 45
        q += 1
    digits = 0
    value = 0
    too_big = False
    while q < n and 48 <= cps[q] <= 57:
        value = value * 10 + (cps[q] - 48)
        if value > 0x10FFFF:
            too_big = True
            value = 0x110000
        digits += 1
        q += 1
    if digits == 0:
        return None
    while q < n and is_white(cps[q]):
        q += 1
    if q >= n or cps[q] != 59:
        return None
    if too_big:
        return None
    if negative and value != 0:
        return None
    if not valid_code_point(value):
        return None
    return (value, q + 1)


def html_decode(text: str) -> str:
    """Named references from the entity table, numeric references by the grammar above. A
    reference that does not match stays as typed."""
    cps = [ord(ch) for ch in text]
    n = len(cps)
    out: list[str] = []
    i = 0
    while i < n:
        c = cps[i]
        if c != 38:
            out.append(chr(c))
            i += 1
            continue
        if i + 1 < n and cps[i + 1] == 35:
            numeric = numeric_ref(cps, i + 2)
            if numeric is not None:
                out.append(chr(numeric[0]))
                i = numeric[1]
                continue
            out.append(chr(c))
            i += 1
            continue
        q = i + 1
        while q < n and ((48 <= cps[q] <= 57) or (65 <= cps[q] <= 90) or (97 <= cps[q] <= 122)):
            q += 1
        if q > i + 1 and q < n and cps[q] == 59:
            code = entity_code_point("".join(chr(x) for x in cps[i + 1 : q]))
            if code >= 0:
                out.append(chr(code))
                i = q + 1
                continue
        out.append(chr(c))
        i += 1
    return "".join(out)


def title_of(html: str) -> Optional[str]:
    """The first <title ...>...</title> with no tag inside it, its entities decoded and its white
    space collapsed. None when there is no such title or it is empty."""
    b = html.encode("utf-8")
    n = len(b)

    def matches(at: int, word: str) -> bool:
        w = word.encode("ascii")
        if at + len(w) > n:
            return False
        return all(_lower(b[at + k]) == _lower(w[k]) for k in range(len(w)))

    for start in range(n):
        if not matches(start, "<title"):
            continue
        close = -1
        for k in range(start + 6, n):
            if b[k] == 62:
                close = k
                break
        if close < 0:
            continue
        lt = -1
        for k in range(close + 1, n):
            if b[k] == 60:
                lt = k
                break
        if lt < 0:
            continue
        if not matches(lt, "</title>"):
            continue
        raw = b[close + 1 : lt].decode("utf-8", errors="ignore")
        decoded = html_decode(raw)
        collapsed: list[str] = []
        pending = False
        for ch in decoded:
            if is_space(ord(ch)):
                pending = True
            else:
                if pending and len(collapsed) > 0:
                    collapsed.append(" ")
                pending = False
                collapsed.append(ch)
        title = "".join(collapsed)
        if len(title.encode("utf-8")) == 0:
            return None
        return title
    return None


def _lower(c: int) -> int:
    return c + 32 if 65 <= c <= 90 else c
