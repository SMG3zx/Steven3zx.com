# Web addresses for the portal: what System.Uri.TryCreate(url, UriKind.Absolute) accepts,
# and the Host and Port the portal reads from it, plus the path a request asks for.
# The reference is src/Portal.cs (IsWebAddress, HostOf, LocalPortOf) and the .NET 10
# behaviour recorded in mojo/py/vinci_diff/portal_truth/ (the cases are in the harness).
#
# Only web addresses from the journal and from portal.json reach the page through this
# parser. Schemes other than http, https and ftp get no default port, so a hand-edited
# mailto or file address can show another host or port than .NET would.

from jsonline import ascii_lower
from session import is_space
from text import code_points_to_string, utf8_code_points


@fieldwise_init
struct Address(Copyable, Movable):
    """The parts of an absolute address the portal reads. port is -1 when there is none.
    """

    var scheme: String
    var host: String
    var port: Int


def byte_range(s: String, start: Int, end: Int) -> String:
    """The bytes [start, end) of s. Callers cut only at ASCII positions."""
    var src = s.as_bytes()
    var out = List[UInt8]()
    for i in range(start, end):
        out.append(src[i])
    return String(unsafe_from_utf8=out)


def code_points_range(cps: List[Int], start: Int, end: Int) raises -> String:
    var part = List[Int]()
    for i in range(start, end):
        part.append(cps[i])
    return code_points_to_string(part)


def is_alpha(c: Int) -> Bool:
    return (c >= 65 and c <= 90) or (c >= 97 and c <= 122)


def is_digit(c: Int) -> Bool:
    return c >= 48 and c <= 57


def is_alnum(c: Int) -> Bool:
    return is_alpha(c) or is_digit(c)


def is_hex(c: Int) -> Bool:
    return is_digit(c) or (c >= 65 and c <= 70) or (c >= 97 and c <= 102)


def hex_digit_value(c: Int) -> Int:
    if is_digit(c):
        return c - 48
    if c >= 97:
        return c - 87
    return c - 55


def default_port(scheme: String) -> Int:
    if scheme == "http":
        return 80
    if scheme == "https":
        return 443
    if scheme == "ftp":
        return 21
    return -1


def index_of(cps: List[Int], start: Int, end: Int, want: Int) -> Int:
    """The first index in [start, end) holding want, or -1."""
    for i in range(start, end):
        if cps[i] == want:
            return i
    return -1


def last_index_of(cps: List[Int], start: Int, end: Int, want: Int) -> Int:
    var found = -1
    for i in range(start, end):
        if cps[i] == want:
            found = i
    return found


def ipv4_part(text: String) -> Int:
    """One dotted part read as inet_aton reads it: decimal, 0x hex or 0 octal. -1 if not one.
    """
    var b = text.as_bytes()
    var n = len(b)
    if n == 0:
        return -1
    var base = 10
    var start = 0
    if n > 2 and b[0] == 48 and (b[1] == 120 or b[1] == 88):
        base = 16
        start = 2
    elif n > 1 and b[0] == 48:
        base = 8
        start = 1
    var value = 0
    for i in range(start, n):
        var c = Int(b[i])
        var digit: Int
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


def canonical_ipv4(host: String) -> String:
    """The dotted form of a host written as 1 to 4 numbers (127.1 is 127.0.0.1), else host.
    """
    var parts = List[String]()
    var b = host.as_bytes()
    var start = 0
    for i in range(len(b) + 1):
        if i == len(b) or b[i] == 46:
            parts.append(byte_range(host, start, i))
            start = i + 1
    var count = len(parts)
    if count < 1 or count > 4:
        return host
    var values = List[Int]()
    for p in parts:
        var v = ipv4_part(p)
        if v < 0:
            return host
        values.append(v)
    for i in range(count - 1):
        if values[i] > 255:
            return host
    var last_limit = 1
    for _ in range(5 - count):
        last_limit *= 256
    if values[count - 1] >= last_limit:
        return host
    var value = values[count - 1]
    for i in range(count - 1):
        value += values[i] * (1 << (8 * (3 - i)))
    return (
        String(value >> 24)
        + "."
        + String((value >> 16) & 255)
        + "."
        + String((value >> 8) & 255)
        + "."
        + String(value & 255)
    )


def parse_address(text: String) raises -> Optional[Address]:
    """System.Uri.TryCreate(text, Absolute), for the parts the portal uses.

    None where .NET says the text is not an absolute address. Leading and trailing white
    space is ignored, as .NET ignores it.
    """
    var points = utf8_code_points(text)
    var lo = 0
    var hi = len(points)
    while lo < hi and is_space(points[lo]):
        lo += 1
    while hi > lo and is_space(points[hi - 1]):
        hi -= 1
    var cps = List[Int]()
    for i in range(lo, hi):
        cps.append(points[i])
    var n = len(cps)
    if n > 0 and cps[0] == 47:
        # .NET reads a leading slash as a file address: "/x" has no host, "//host/x" has one.
        if n == 1 or cps[1] != 47:
            return Optional(Address("file", "", -1))
        return _authority(cps, 2, "file", -1)
    if n == 0 or not is_alpha(cps[0]):
        return None
    var colon = 1
    while colon < n and (
        is_alnum(cps[colon])
        or cps[colon] == 43
        or cps[colon] == 45
        or cps[colon] == 46
    ):
        colon += 1
    if colon >= n or cps[colon] != 58:
        return None
    var scheme = ascii_lower(code_points_range(cps, 0, colon))
    var after = colon + 1
    if after + 1 < n and cps[after] == 47 and cps[after + 1] == 47:
        return _authority(cps, after + 2, scheme, default_port(scheme))
    if scheme == "http" or scheme == "https":
        return None
    if scheme == "mailto":
        # .NET reads the host after the last @ and uses the SMTP port.
        var at_sign = last_index_of(cps, after, n, 64)
        if at_sign < 0:
            return Optional(Address(scheme, "", 25))
        return Optional(
            Address(
                scheme, ascii_lower(code_points_range(cps, at_sign + 1, n)), 25
            )
        )
    return Optional(Address(scheme, "", -1))


def _authority(
    cps: List[Int], first: Int, scheme: String, no_port: Int
) raises -> Optional[Address]:
    """The host and port that follow a //, up to the path, query or fragment. no_port is
    the port when the text names none (the scheme's default)."""
    var n = len(cps)
    var end = first
    while end < n and cps[end] != 47 and cps[end] != 63 and cps[end] != 35:
        end += 1
    var at = last_index_of(cps, first, end, 64)
    var host_start = first
    if at >= 0:
        host_start = at + 1
    var host_text: String
    # The port text is [port_from, port_to); port_from is -1 when the address has no colon.
    var port_from = -1
    var port_to = -1
    if host_start < end and cps[host_start] == 91:
        var close = index_of(cps, host_start, end, 93)
        if close < 0:
            return None
        host_text = code_points_range(cps, host_start, close + 1)
        if close + 1 < end:
            if cps[close + 1] != 58:
                return None
            port_from = close + 2
            port_to = end
    else:
        var host_end = end
        var colon_at = index_of(cps, host_start, end, 58)
        if colon_at >= 0:
            if scheme == "file":
                # A file address takes no port, so .NET does not read one with a colon.
                return None
            host_end = colon_at
            port_from = colon_at + 1
            port_to = end
        for i in range(host_start, host_end):
            var c = cps[i]
            if not (is_alnum(c) or c == 45 or c == 46 or c == 95 or c >= 128):
                return None
        host_text = code_points_range(cps, host_start, host_end)
    if host_text.byte_length() == 0:
        return None
    var port = no_port
    if port_from >= 0 and port_from < port_to:
        var value = 0
        for i in range(port_from, port_to):
            if not is_digit(cps[i]):
                return None
            value = value * 10 + (cps[i] - 48)
            if value > 65535:
                return None
        port = value
    elif port_from >= 0 and port_from > port_to:
        return None
    var host = ascii_lower(host_text)
    if not host.startswith("["):
        host = canonical_ipv4(host)
    return Optional(Address(scheme, host, port))


def is_web_address(url: String) raises -> Bool:
    """Portal.IsWebAddress: an absolute http or https address."""
    var parsed = parse_address(url)
    if parsed is None:
        return False
    var scheme = parsed.value().scheme
    return scheme == "http" or scheme == "https"


def host_of(url: String) raises -> String:
    """Portal.HostOf: the host without a leading www., or the text itself when it is not
    an address."""
    var parsed = parse_address(url)
    if parsed is None:
        return url
    var host = parsed.value().host
    if host.startswith("www."):
        return byte_range(host, 4, host.byte_length())
    return host


def local_port_of(url: String) raises -> Optional[Int]:
    """Portal.LocalPortOf: the port when the address names localhost, 127.0.0.1 or [::1].
    """
    var parsed = parse_address(url)
    if parsed is None:
        return None
    var address = parsed.value().copy()
    if (
        address.host == "localhost"
        or address.host == "127.0.0.1"
        or address.host == "[::1]"
    ):
        return Optional(address.port)
    return None


def unreserved(c: Int) -> Bool:
    return is_alnum(c) or c == 45 or c == 46 or c == 95 or c == 126


def raw_allowed(c: Int) -> Bool:
    """The ASCII that .NET leaves as it is in a path. Everything else is percent-escaped.
    """
    if is_alnum(c):
        return True
    for allowed in "-._~!$&'()*+,;=:@/[]".as_bytes():
        if Int(allowed) == c:
            return True
    return False


def upper_hex_pair(value: Int) -> String:
    """Two uppercase hex digits for one byte."""
    var digits = "0123456789ABCDEF".as_bytes()
    var out = List[UInt8]()
    out.append(digits[(value >> 4) & 15])
    out.append(digits[value & 15])
    return String(unsafe_from_utf8=out)


def escape_path(raw: String) -> String:
    """The path .NET keeps. A %XX escape of an unreserved character is decoded; any other
    escape stays as typed; a lone % becomes %25; a backslash is a slash; every other byte
    outside the allowed ASCII is escaped with uppercase hex."""
    var b = raw.as_bytes()
    var n = len(b)
    var out = List[UInt8]()
    var i = 0
    while i < n:
        var c = Int(b[i])
        if c == 92:
            out.append(47)
            i += 1
        elif (
            c == 37
            and i + 2 < n
            and is_hex(Int(b[i + 1]))
            and is_hex(Int(b[i + 2]))
        ):
            var value = hex_digit_value(Int(b[i + 1])) * 16 + hex_digit_value(
                Int(b[i + 2])
            )
            if unreserved(value):
                out.append(UInt8(value))
            else:
                out.append(37)
                out.append(b[i + 1])
                out.append(b[i + 2])
            i += 3
        elif c == 37:
            out.append(37)
            out.append(50)
            out.append(53)
            i += 1
        elif raw_allowed(c):
            out.append(UInt8(c))
            i += 1
        else:
            for x in ("%" + upper_hex_pair(c)).as_bytes():
                out.append(x)
            i += 1
    return String(unsafe_from_utf8=out)


def drop_last_segment(path: String) -> String:
    """RFC 3986 "remove the last segment and its preceding /" from the output.
    """
    var b = path.as_bytes()
    var cut = 0
    for i in range(len(b)):
        if b[i] == 47:
            cut = i
    return byte_range(path, 0, cut)


def remove_dot_segments(path: String) -> String:
    """RFC 3986 section 5.2.4, the way .NET resolves "." and ".." in a request path.
    """
    var input = path
    var output = String("")
    while input.byte_length() > 0:
        var n = input.byte_length()
        if input.startswith("../"):
            input = byte_range(input, 3, n)
        elif input.startswith("./"):
            input = byte_range(input, 2, n)
        elif input.startswith("/./"):
            input = byte_range(input, 2, n)
        elif input == "/.":
            input = "/"
        elif input.startswith("/../"):
            input = byte_range(input, 3, n)
            output = drop_last_segment(output)
        elif input == "/..":
            input = "/"
            output = drop_last_segment(output)
        elif input == "." or input == "..":
            input = ""
        else:
            var from_index = 1 if input.startswith("/") else 0
            var cut = n
            for i in range(from_index, n):
                if input.as_bytes()[i] == 47:
                    cut = i
                    break
            output += byte_range(input, 0, cut)
            input = byte_range(input, cut, n)
    return output


def request_path(target: String) -> String:
    """Request.Url.AbsolutePath for an origin-form request target: no query, no fragment.
    """
    var b = target.as_bytes()
    var end = len(b)
    for i in range(len(b)):
        if b[i] == 63 or b[i] == 35:
            end = i
            break
    return remove_dot_segments(escape_path(byte_range(target, 0, end)))
