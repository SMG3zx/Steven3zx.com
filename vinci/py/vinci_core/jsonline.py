"""The JSON of vinci's journal, one object per line.

The journal is an append-only file that already holds lines in this format, so writing
keeps it byte for byte: HTML-safe escaping, shortest round-trip floats (docs/mojo-gotchas.md).
Reading matches field names without regard to case and skips unknown fields.

Port of mojo/src/jsonline.mojo.
"""

from __future__ import annotations

from .text import decimal_digits, fixed_text, utf8_code_points
from .timeutil import Instant, ascii_prefix, format_instant

QUOTE = 34
BACKSLASH = 92
LBRACE = 123
RBRACE = 125
LBRACKET = 91
RBRACKET = 93
COLON = 58
COMMA = 44
MINUS = 45
PLUS = 43
DOT = 46
ZERO = 48
NINE = 57
SPACE = 32
TAB = 9
LF = 10
CR = 13


def hex4(code: int) -> str:
    """Four uppercase hex digits, as the journal writes \\uXXXX."""
    return "".join("0123456789ABCDEF"[(code >> (12 - 4 * k)) & 15] for k in range(4))


def unicode_escape(unit: int) -> str:
    return "\\u" + hex4(unit)


def json_string(s: str) -> str:
    """A JSON string literal with the default encoder's escapes.

    Quotes, backslashes and control characters use the short forms where JSON has them.
    Every non-ASCII character becomes \\uXXXX (a surrogate pair when astral), and so do
    < > & ' + and backtick, which the encoder treats as unsafe in HTML. / is left alone.
    """
    out = ['"']
    for cp in utf8_code_points(s):
        if cp == QUOTE:
            out.append(unicode_escape(34))
        elif cp == BACKSLASH:
            out.append("\\\\")
        elif cp == LF:
            out.append("\\n")
        elif cp == CR:
            out.append("\\r")
        elif cp == TAB:
            out.append("\\t")
        elif cp == 8:
            out.append("\\b")
        elif cp == 12:
            out.append("\\f")
        elif cp < 0x20 or cp == 0x7F or cp >= 0x80:
            if cp >= 0x10000:
                v = cp - 0x10000
                out.append(unicode_escape(0xD800 + (v >> 10)))
                out.append(unicode_escape(0xDC00 + (v & 0x3FF)))
            else:
                out.append(unicode_escape(cp))
        elif cp in (60, 62, 38, 39, 43, 96):
            # < > & ' + `
            out.append(unicode_escape(cp))
        else:
            out.append(chr(cp))
    out.append('"')
    return "".join(out)


def json_instant(at: Instant) -> str:
    return '"' + format_instant(at) + '"'


def format_json_double(v: float) -> str:
    """A double as the journal writes it: shortest round-trip digits.

    Plain notation when the decimal exponent is from -4 to 16, otherwise E notation with a
    sign and at least two exponent digits (1E-07, 1E+21).
    """
    if v != v or v in (float("inf"), float("-inf")):
        raise ValueError("json: a double must be finite")
    d = decimal_digits(v)
    if len(d.digits) == 0:
        return "-0" if d.negative else "0"
    exponent = d.point - 1
    if -4 <= exponent < 17:
        return fixed_text(d)
    sign = "-" if d.negative else ""
    body = ascii_prefix(d.digits, 1)
    if len(d.digits) > 1:
        body += "." + d.digits[1:]
    mag = -exponent if exponent < 0 else exponent
    exp_text = str(mag) if mag >= 10 else "0" + str(mag)
    return sign + body + "E" + ("-" if exponent < 0 else "+") + exp_text


def is_space(c: int) -> bool:
    return c in (SPACE, TAB, LF, CR)


def hex_value(c: int) -> int:
    if ZERO <= c <= NINE:
        return c - ZERO
    if 65 <= c <= 70:
        return c - 55
    if 97 <= c <= 102:
        return c - 87
    raise ValueError("json: bad hex digit in a \\u escape")


class Cursor:
    """A byte cursor over one JSON line. Errors name a byte offset, not a line."""

    def __init__(self, line: str) -> None:
        self.data: bytes = line.encode("utf-8")
        self.pos: int = 0

    def at_end(self) -> bool:
        return self.pos >= len(self.data)

    def byte_at(self, i: int) -> int:
        return self.data[i]

    def peek(self) -> int:
        if self.at_end():
            raise ValueError("json: line ended early")
        return self.byte_at(self.pos)

    def take(self) -> int:
        c = self.peek()
        self.pos += 1
        return c

    def skip_space(self) -> None:
        while not self.at_end() and is_space(self.byte_at(self.pos)):
            self.pos += 1

    def expect(self, want: int) -> None:
        got = self.take()
        if got != want:
            raise ValueError(f"json: unexpected byte at {self.pos - 1}")

    def at_null(self) -> bool:
        return not self.at_end() and self.byte_at(self.pos) == 110  # n

    def skip_null(self) -> None:
        for c in b"null":
            self.expect(c)

    def hex4(self) -> int:
        code = 0
        for _ in range(4):
            code = code * 16 + hex_value(self.take())
        return code

    def read_string(self) -> str:
        """A JSON string with its escapes decoded to UTF-8.

        A lone surrogate is refused. The journal never holds one: writing replaces them
        with U+FFFD.
        """
        self.expect(QUOTE)
        out = bytearray()
        while True:
            c = self.take()
            if c == QUOTE:
                break
            if c < 0x20:
                raise ValueError("json: control character inside a string")
            if c != BACKSLASH:
                out.append(c)
                continue
            e = self.take()
            if e == 117:  # u
                code = self.hex4()
                if 0xDC00 <= code <= 0xDFFF:
                    raise ValueError("json: lone low surrogate")
                if 0xD800 <= code <= 0xDBFF:
                    if not (
                        self.pos + 1 < len(self.data)
                        and self.byte_at(self.pos) == BACKSLASH
                        and self.byte_at(self.pos + 1) == 117
                    ):
                        raise ValueError("json: lone high surrogate")
                    self.pos += 2
                    low = self.hex4()
                    if low < 0xDC00 or low > 0xDFFF:
                        raise ValueError("json: high surrogate without a low one")
                    code = 0x10000 + (code - 0xD800) * 1024 + (low - 0xDC00)
                out += chr(code).encode("utf-8")
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
            elif e in (QUOTE, BACKSLASH, 47):  # " \ /
                out.append(e)
            else:
                raise ValueError("json: bad escape")
        return out.decode("utf-8")

    def read_number(self) -> float:
        """A JSON number, converted with the correctly rounded parser."""
        start = self.pos
        if not self.at_end() and self.byte_at(self.pos) == MINUS:
            self.pos += 1
        digits = 0
        while not self.at_end() and ZERO <= self.byte_at(self.pos) <= NINE:
            self.pos += 1
            digits += 1
        if digits == 0:
            raise ValueError("json: expected a number")
        if not self.at_end() and self.byte_at(self.pos) == DOT:
            self.pos += 1
            frac = 0
            while not self.at_end() and ZERO <= self.byte_at(self.pos) <= NINE:
                self.pos += 1
                frac += 1
            if frac == 0:
                raise ValueError("json: a fraction needs digits")
        if not self.at_end() and self.byte_at(self.pos) in (101, 69):
            self.pos += 1
            if not self.at_end() and self.byte_at(self.pos) in (PLUS, MINUS):
                self.pos += 1
            exp_digits = 0
            while not self.at_end() and ZERO <= self.byte_at(self.pos) <= NINE:
                self.pos += 1
                exp_digits += 1
            if exp_digits == 0:
                raise ValueError("json: an exponent needs digits")
        return float(self.data[start : self.pos].decode("ascii"))

    def skip_value(self) -> None:
        c = self.peek()
        if c == QUOTE:
            self.read_string()
            return
        if c in (LBRACE, LBRACKET):
            depth = 0
            while True:
                b = self.peek()
                if b == QUOTE:
                    self.read_string()
                    continue
                self.pos += 1
                if b in (LBRACE, LBRACKET):
                    depth += 1
                elif b in (RBRACE, RBRACKET):
                    depth -= 1
                    if depth == 0:
                        return
        # A number, true, false or null: up to the next separator.
        while not self.at_end():
            b = self.byte_at(self.pos)
            if b in (COMMA, RBRACE, RBRACKET) or is_space(b):
                break
            self.pos += 1

    def read_string_array(self) -> list[str]:
        out: list[str] = []
        self.expect(LBRACKET)
        self.skip_space()
        if not self.at_end() and self.byte_at(self.pos) == RBRACKET:
            self.pos += 1
            return out
        while True:
            self.skip_space()
            out.append(self.read_string())
            self.skip_space()
            sep = self.take()
            if sep == RBRACKET:
                break
            if sep != COMMA:
                raise ValueError("json: expected , or ] in an array")
        return out


def ascii_lower(s: str) -> str:
    return "".join(chr(ord(c) + 32) if "A" <= c <= "Z" else c for c in s)
