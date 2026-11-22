# The JSON of vinci's journal, one object per line. The journal is an append-only file that
# already holds lines in this format, so writing keeps it byte for byte:
# HTML-safe escaping, shortest round-trip floats (docs/mojo-gotchas.md). Reading
# matches field names without regard to case and skips unknown fields.

from text import (
    ascii_tail,
    decimal_digits,
    fixed_text,
    utf8_append,
    utf8_code_points,
)
from timeutil import Instant, ascii_prefix, format_instant

comptime QUOTE = 34
comptime BACKSLASH = 92
comptime LBRACE = 123
comptime RBRACE = 125
comptime LBRACKET = 91
comptime RBRACKET = 93
comptime COLON = 58
comptime COMMA = 44
comptime MINUS = 45
comptime PLUS = 43
comptime DOT = 46
comptime ZERO = 48
comptime NINE = 57
comptime SPACE = 32
comptime TAB = 9
comptime LF = 10
comptime CR = 13


def hex4(code: Int) -> String:
    """Four uppercase hex digits, as the journal writes \\uXXXX."""
    var hex = "0123456789ABCDEF".as_bytes()
    var out = List[UInt8]()
    for k in range(4):
        out.append(hex[(code >> (12 - 4 * k)) & 15])
    return String(unsafe_from_utf8=out)


def unicode_escape(unit: Int) -> String:
    return "\\u" + hex4(unit)


def json_string(s: String) raises -> String:
    """A JSON string literal with the default encoder's escapes.

    Quotes, backslashes and control characters use the short forms where JSON
    has them. Every non-ASCII character becomes \\uXXXX (a surrogate pair when
    astral), and so do < > & ' + and backtick, which the encoder treats as
    unsafe in HTML. / is left alone.
    """
    var out = String('"')
    for cp in utf8_code_points(s):
        if cp == QUOTE:
            out += unicode_escape(34)
        elif cp == BACKSLASH:
            out += "\\\\"
        elif cp == LF:
            out += "\\n"
        elif cp == CR:
            out += "\\r"
        elif cp == TAB:
            out += "\\t"
        elif cp == 8:
            out += "\\b"
        elif cp == 12:
            out += "\\f"
        elif cp < 0x20 or cp == 0x7F or cp >= 0x80:
            if cp >= 0x10000:
                var v = cp - 0x10000
                out += unicode_escape(0xD800 + (v >> 10))
                out += unicode_escape(0xDC00 + (v & 0x3FF))
            else:
                out += unicode_escape(cp)
        elif (
            cp == 60 or cp == 62 or cp == 38 or cp == 39 or cp == 43 or cp == 96
        ):
            # < > & ' + `
            out += unicode_escape(cp)
        else:
            var one = List[UInt8]()
            utf8_append(one, cp)
            out += String(unsafe_from_utf8=one)
    return out + '"'


def json_instant(at: Instant) -> String:
    return '"' + format_instant(at) + '"'


def format_json_double(v: Float64) raises -> String:
    """A double as the journal writes it: shortest round-trip digits.

    Plain notation when the decimal exponent is from -4 to 16, otherwise
    E notation with a sign and at least two exponent digits (1E-07, 1E+21).
    """
    if v != v or v > Float64.MAX or v < -Float64.MAX:
        raise Error("json: a double must be finite")
    var d = decimal_digits(v)
    if d.digits.byte_length() == 0:
        return "-0" if d.negative else "0"
    var exponent = d.point - 1
    if exponent >= -4 and exponent < 17:
        return fixed_text(d)
    var sign = "-" if d.negative else ""
    var body = ascii_prefix(d.digits, 1)
    if d.digits.byte_length() > 1:
        body += "." + ascii_tail(d.digits, 1)
    var mag = -exponent if exponent < 0 else exponent
    var exp_text = String(mag) if mag >= 10 else "0" + String(mag)
    return sign + body + "E" + ("-" if exponent < 0 else "+") + exp_text


def is_space(c: Int) -> Bool:
    return c == SPACE or c == TAB or c == LF or c == CR


def hex_value(c: Int) raises -> Int:
    if c >= ZERO and c <= NINE:
        return c - ZERO
    if c >= 65 and c <= 70:
        return c - 55
    if c >= 97 and c <= 102:
        return c - 87
    raise Error("json: bad hex digit in a \\u escape")


struct Cursor(Movable):
    """A byte cursor over one JSON line. Errors name a byte offset, not a line.
    """

    var data: List[UInt8]
    var pos: Int

    def __init__(out self, line: String):
        self.data = List[UInt8]()
        for b in line.as_bytes():
            self.data.append(b)
        self.pos = 0

    def at_end(self) -> Bool:
        return self.pos >= len(self.data)

    def byte_at(self, i: Int) -> Int:
        return Int(self.data[i])

    def peek(self) raises -> Int:
        if self.at_end():
            raise Error("json: line ended early")
        return self.byte_at(self.pos)

    def take(mut self) raises -> Int:
        var c = self.peek()
        self.pos += 1
        return c

    def skip_space(mut self):
        while not self.at_end() and is_space(self.byte_at(self.pos)):
            self.pos += 1

    def expect(mut self, want: Int) raises:
        var got = self.take()
        if got != want:
            raise Error("json: unexpected byte at " + String(self.pos - 1))

    def at_null(self) -> Bool:
        return not self.at_end() and self.byte_at(self.pos) == 110  # n

    def skip_null(mut self) raises:
        for c in "null".as_bytes():
            self.expect(Int(c))

    def hex4(mut self) raises -> Int:
        var code = 0
        for _ in range(4):
            code = code * 16 + hex_value(self.take())
        return code

    def read_string(mut self) raises -> String:
        """A JSON string with its escapes decoded to UTF-8.

        A lone surrogate is refused. The journal never holds one: writing
        replaces them with U+FFFD.
        """
        self.expect(QUOTE)
        var out = List[UInt8]()
        while True:
            var c = self.take()
            if c == QUOTE:
                break
            if c < 0x20:
                raise Error("json: control character inside a string")
            if c != BACKSLASH:
                out.append(UInt8(c))
                continue
            var e = self.take()
            if e == 117:  # u
                var code = self.hex4()
                if code >= 0xDC00 and code <= 0xDFFF:
                    raise Error("json: lone low surrogate")
                if code >= 0xD800 and code <= 0xDBFF:
                    if not (
                        self.pos + 1 < len(self.data)
                        and self.byte_at(self.pos) == BACKSLASH
                        and self.byte_at(self.pos + 1) == 117
                    ):
                        raise Error("json: lone high surrogate")
                    self.pos += 2
                    var low = self.hex4()
                    if low < 0xDC00 or low > 0xDFFF:
                        raise Error("json: high surrogate without a low one")
                    code = 0x10000 + (code - 0xD800) * 1024 + (low - 0xDC00)
                utf8_append(out, code)
            elif e == 110:  # n
                out.append(10)
            elif e == 116:  # t
                out.append(9)
            elif e == 114:  # r
                out.append(13)
            elif e == 98:  # b
                out.append(8)
            elif e == 102:  # f
                out.append(12)
            elif e == QUOTE or e == BACKSLASH or e == 47:  # " \ /
                out.append(UInt8(e))
            else:
                raise Error("json: bad escape")
        return String(unsafe_from_utf8=out)

    def read_number(mut self) raises -> Float64:
        """A JSON number, converted with the correctly rounded parser in Mojo.
        """
        var start = self.pos
        if not self.at_end() and self.byte_at(self.pos) == MINUS:
            self.pos += 1
        var digits = 0
        while (
            not self.at_end()
            and self.byte_at(self.pos) >= ZERO
            and self.byte_at(self.pos) <= NINE
        ):
            self.pos += 1
            digits += 1
        if digits == 0:
            raise Error("json: expected a number")
        if not self.at_end() and self.byte_at(self.pos) == DOT:
            self.pos += 1
            var frac = 0
            while (
                not self.at_end()
                and self.byte_at(self.pos) >= ZERO
                and self.byte_at(self.pos) <= NINE
            ):
                self.pos += 1
                frac += 1
            if frac == 0:
                raise Error("json: a fraction needs digits")
        if not self.at_end() and (
            self.byte_at(self.pos) == 101 or self.byte_at(self.pos) == 69
        ):
            self.pos += 1
            if not self.at_end() and (
                self.byte_at(self.pos) == PLUS
                or self.byte_at(self.pos) == MINUS
            ):
                self.pos += 1
            var exp_digits = 0
            while (
                not self.at_end()
                and self.byte_at(self.pos) >= ZERO
                and self.byte_at(self.pos) <= NINE
            ):
                self.pos += 1
                exp_digits += 1
            if exp_digits == 0:
                raise Error("json: an exponent needs digits")
        var token = String(
            unsafe_from_utf8=_slice_range(self.data, start, self.pos)
        )
        return Float64(token)

    def skip_value(mut self) raises:
        var c = self.peek()
        if c == QUOTE:
            _ = self.read_string()
            return
        if c == LBRACE or c == LBRACKET:
            var depth = 0
            while True:
                var b = self.peek()
                if b == QUOTE:
                    _ = self.read_string()
                    continue
                self.pos += 1
                if b == LBRACE or b == LBRACKET:
                    depth += 1
                elif b == RBRACE or b == RBRACKET:
                    depth -= 1
                    if depth == 0:
                        return
        # A number, true, false or null: up to the next separator.
        while not self.at_end():
            var b = self.byte_at(self.pos)
            if b == COMMA or b == RBRACE or b == RBRACKET or is_space(b):
                break
            self.pos += 1

    def read_string_array(mut self) raises -> List[String]:
        var out = List[String]()
        self.expect(LBRACKET)
        self.skip_space()
        if not self.at_end() and self.byte_at(self.pos) == RBRACKET:
            self.pos += 1
            return out^
        while True:
            self.skip_space()
            out.append(self.read_string())
            self.skip_space()
            var sep = self.take()
            if sep == RBRACKET:
                break
            if sep != COMMA:
                raise Error("json: expected , or ] in an array")
        return out^


def _slice_range(src: List[UInt8], start: Int, end: Int) -> List[UInt8]:
    var out = List[UInt8]()
    for i in range(start, end):
        out.append(src[i])
    return out^


def ascii_lower(s: String) -> String:
    var src = s.as_bytes()
    var out = List[UInt8]()
    for c in src:
        if c >= 65 and c <= 90:
            out.append(c + 32)
        else:
            out.append(c)
    return String(unsafe_from_utf8=out)
