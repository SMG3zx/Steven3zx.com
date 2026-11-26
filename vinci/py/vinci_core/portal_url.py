"""Web addresses for the portal: which text counts as an absolute address, and the host and port
the portal reads from it, plus the path a request asks for.

Only web addresses from the journal and from portal.json reach the page through this parser.
Schemes other than http, https and ftp get no default port, so a hand-edited mailto or file
address can show an unexpected host or port.

Port of mojo/src/portal_url.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from .jsonline import ascii_lower
from .session import is_space


@dataclass(frozen=True)
class Address:
    """The parts of an absolute address the portal reads. port is -1 when there is none."""

    scheme: str
    host: str
    port: int


def byte_range(s: str, start: int, end: int) -> str:
    """The bytes [start, end) of s. Callers cut only at ASCII positions."""
    return s.encode("utf-8")[start:end].decode("utf-8", errors="ignore")


def is_alpha(c: int) -> bool:
    return (65 <= c <= 90) or (97 <= c <= 122)


def is_digit(c: int) -> bool:
    return 48 <= c <= 57


def is_alnum(c: int) -> bool:
    return is_alpha(c) or is_digit(c)


def is_hex(c: int) -> bool:
    return is_digit(c) or (65 <= c <= 70) or (97 <= c <= 102)


def hex_digit_value(c: int) -> int:
    if is_digit(c):
        return c - 48
    if c >= 97:
        return c - 87
    return c - 55


def default_port(scheme: str) -> int:
    if scheme == "http":
        return 80
    if scheme == "https":
        return 443
    if scheme == "ftp":
        return 21
    return -1


def index_of(cps: list[int], start: int, end: int, want: int) -> int:
    """The first index in [start, end) holding want, or -1."""
    for i in range(start, end):
        if cps[i] == want:
            return i
    return -1


def last_index_of(cps: list[int], start: int, end: int, want: int) -> int:
    found = -1
    for i in range(start, end):
        if cps[i] == want:
            found = i
    return found


def ipv4_part(text: str) -> int:
    """One dotted part read as inet_aton reads it: decimal, 0x hex or 0 octal. -1 if not one."""
    b = text.encode("utf-8")
    n = len(b)
    if n == 0:
        return -1
    base = 10
    start = 0
    if n > 2 and b[0] == 48 and b[1] in (120, 88):
        base = 16
        start = 2
    elif n > 1 and b[0] == 48:
        base = 8
        start = 1
    value = 0
    for i in range(start, n):
        c = b[i]
        if base == 16:
            if not is_hex(c):
                return -1
            digit = hex_digit_value(c)
        else:
            if not is_digit(c):
                return -1
            digit = c - 48
            if base == 8 and digit > 7:
                return -1
        value = value * base + digit
        if value > 0xFFFFFFFF:
            return -1
    return value


def canonical_ipv4(host: str) -> str:
    """The dotted form of a host written as 1 to 4 numbers (127.1 is 127.0.0.1), else host."""
    parts = host.split(".")
    count = len(parts)
    if count < 1 or count > 4:
        return host
    values: list[int] = []
    for p in parts:
        v = ipv4_part(p)
        if v < 0:
            return host
        values.append(v)
    for i in range(count - 1):
        if values[i] > 255:
            return host
    last_limit = 1
    for _ in range(5 - count):
        last_limit *= 256
    if values[count - 1] >= last_limit:
        return host
    value = values[count - 1]
    for i in range(count - 1):
        value += values[i] * (1 << (8 * (3 - i)))
    return (
        str(value >> 24) + "." + str((value >> 16) & 255) + "." + str((value >> 8) & 255) + "." + str(value & 255)
    )


def _authority(cps: list[int], first: int, scheme: str, no_port: int) -> Optional[Address]:
    """The host and port that follow a //, up to the path, query or fragment. no_port is the
    port when the text names none (the scheme's default)."""
    n = len(cps)
    end = first
    while end < n and cps[end] not in (47, 63, 35):
        end += 1
    at = last_index_of(cps, first, end, 64)
    host_start = first
    if at >= 0:
        host_start = at + 1
    # The port text is [port_from, port_to); port_from is -1 when the address has no colon.
    port_from = -1
    port_to = -1
    if host_start < end and cps[host_start] == 91:
        close = index_of(cps, host_start, end, 93)
        if close < 0:
            return None
        host_text = _range(cps, host_start, close + 1)
        if close + 1 < end:
            if cps[close + 1] != 58:
                return None
            port_from = close + 2
            port_to = end
    else:
        host_end = end
        colon_at = index_of(cps, host_start, end, 58)
        if colon_at >= 0:
            if scheme == "file":
                # A file address takes no port, so a colon is not read as one.
                return None
            host_end = colon_at
            port_from = colon_at + 1
            port_to = end
        for i in range(host_start, host_end):
            c = cps[i]
            if not (is_alnum(c) or c in (45, 46, 95) or c >= 128):
                return None
        host_text = _range(cps, host_start, host_end)
    if len(host_text.encode("utf-8")) == 0:
        return None
    port = no_port
    if port_from >= 0 and port_from < port_to:
        value = 0
        for i in range(port_from, port_to):
            if not is_digit(cps[i]):
                return None
            value = value * 10 + (cps[i] - 48)
            if value > 65535:
                return None
        port = value
    elif port_from >= 0 and port_from > port_to:
        return None
    host = ascii_lower(host_text)
    if not host.startswith("["):
        host = canonical_ipv4(host)
    return Address(scheme, host, port)


def _range(cps: list[int], start: int, end: int) -> str:
    return "".join(chr(c) for c in cps[start:end])


def parse_address(text: str) -> Optional[Address]:
    """Parse an absolute address, for the parts the portal uses.

    None where the text is not an absolute address. Leading and trailing white space is ignored.
    """
    points = [ord(ch) for ch in text]
    lo = 0
    hi = len(points)
    while lo < hi and is_space(points[lo]):
        lo += 1
    while hi > lo and is_space(points[hi - 1]):
        hi -= 1
    cps = points[lo:hi]
    n = len(cps)
    if n > 0 and cps[0] == 47:
        # A leading slash is a file address: "/x" has no host, "//host/x" has one.
        if n == 1 or cps[1] != 47:
            return Address("file", "", -1)
        return _authority(cps, 2, "file", -1)
    if n == 0 or not is_alpha(cps[0]):
        return None
    colon = 1
    while colon < n and (is_alnum(cps[colon]) or cps[colon] in (43, 45, 46)):
        colon += 1
    if colon >= n or cps[colon] != 58:
        return None
    scheme = ascii_lower(_range(cps, 0, colon))
    after = colon + 1
    if after + 1 < n and cps[after] == 47 and cps[after + 1] == 47:
        return _authority(cps, after + 2, scheme, default_port(scheme))
    if scheme in ("http", "https"):
        return None
    if scheme == "mailto":
        # The host is read after the last @, with the SMTP port.
        at_sign = last_index_of(cps, after, n, 64)
        if at_sign < 0:
            return Address(scheme, "", 25)
        return Address(scheme, ascii_lower(_range(cps, at_sign + 1, n)), 25)
    return Address(scheme, "", -1)


def is_web_address(url: str) -> bool:
    """Whether the text is an absolute http or https address."""
    parsed = parse_address(url)
    if parsed is None:
        return False
    return parsed.scheme in ("http", "https")


def host_of(url: str) -> str:
    """The host without a leading www., or the text itself when it is not an address."""
    parsed = parse_address(url)
    if parsed is None:
        return url
    host = parsed.host
    if host.startswith("www."):
        return host[4:]
    return host


def local_port_of(url: str) -> Optional[int]:
    """The port when the address names localhost, 127.0.0.1 or [::1]."""
    parsed = parse_address(url)
    if parsed is None:
        return None
    if parsed.host in ("localhost", "127.0.0.1", "[::1]"):
        return parsed.port
    return None


def unreserved(c: int) -> bool:
    return is_alnum(c) or c in (45, 46, 95, 126)


def raw_allowed(c: int) -> bool:
    """The ASCII that stays as it is in a path. Everything else is percent-escaped."""
    if is_alnum(c):
        return True
    return c in b"-._~!$&'()*+,;=:@/[]"


def upper_hex_pair(value: int) -> str:
    digits = "0123456789ABCDEF"
    return digits[(value >> 4) & 15] + digits[value & 15]


def escape_path(raw: str) -> str:
    """The path as the portal keeps it. A %XX escape of an unreserved character is decoded; any
    other escape stays as typed; a lone % becomes %25; a backslash is a slash; every other byte
    outside the allowed ASCII is escaped with uppercase hex."""
    b = raw.encode("utf-8")
    n = len(b)
    out = bytearray()
    i = 0
    while i < n:
        c = b[i]
        if c == 92:
            out.append(47)
            i += 1
        elif c == 37 and i + 2 < n and is_hex(b[i + 1]) and is_hex(b[i + 2]):
            value = hex_digit_value(b[i + 1]) * 16 + hex_digit_value(b[i + 2])
            if unreserved(value):
                out.append(value)
            else:
                out.append(37)
                out.append(b[i + 1])
                out.append(b[i + 2])
            i += 3
        elif c == 37:
            out += b"%25"
            i += 1
        elif raw_allowed(c):
            out.append(c)
            i += 1
        else:
            out += ("%" + upper_hex_pair(c)).encode("ascii")
            i += 1
    return out.decode("ascii")


def drop_last_segment(path: str) -> str:
    """RFC 3986 "remove the last segment and its preceding /" from the output."""
    cut = path.rfind("/")
    if cut < 0:
        cut = 0
    return path[:cut]


def remove_dot_segments(path: str) -> str:
    """RFC 3986 section 5.2.4, for "." and ".." in a request path."""
    text = path
    output = ""
    while len(text) > 0:
        if text.startswith("../"):
            text = text[3:]
        elif text.startswith("./"):
            text = text[2:]
        elif text.startswith("/./"):
            text = text[2:]
        elif text == "/.":
            text = "/"
        elif text.startswith("/../"):
            text = text[3:]
            output = drop_last_segment(output)
        elif text == "/..":
            text = "/"
            output = drop_last_segment(output)
        elif text in (".", ".."):
            text = ""
        else:
            from_index = 1 if text.startswith("/") else 0
            cut = len(text)
            for i in range(from_index, len(text)):
                if text[i] == "/":
                    cut = i
                    break
            output += text[:cut]
            text = text[cut:]
    return output


def request_path(target: str) -> str:
    """Request.Url.AbsolutePath for an origin-form request target: no query, no fragment."""
    end = len(target)
    for i, ch in enumerate(target):
        if ch in "?#":
            end = i
            break
    return remove_dot_segments(escape_path(target[:end]))
