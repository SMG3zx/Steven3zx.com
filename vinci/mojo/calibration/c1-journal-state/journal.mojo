# Reads vinci's journal: one JSON object per line, as Journal.cs writes it.
# Only kind, id, text and value are kept. Every other field is skipped.

from std.pathlib import Path

comptime QUOTE = 34  # "
comptime BACKSLASH = 92  # \
comptime LBRACE = 123  # {
comptime RBRACE = 125  # }
comptime LBRACKET = 91  # [
comptime RBRACKET = 93  # ]
comptime COLON = 58  # :
comptime COMMA = 44  # ,
comptime MINUS = 45  # -
comptime DOT = 46  # .
comptime ZERO = 48  # 0
comptime NINE = 57  # 9
comptime SPACE = 32
comptime TAB = 9
comptime LF = 10
comptime CR = 13


@fieldwise_init
struct Event(Copyable, Movable):
    var kind: String
    var id: String
    var text: String
    var value: Float64
    var has_value: Bool


def is_space(c: Int) -> Bool:
    return c == SPACE or c == TAB or c == LF or c == CR


def hex_value(c: Int) raises -> Int:
    if c >= ZERO and c <= NINE:
        return c - ZERO
    if c >= 65 and c <= 70:
        return c - 55
    if c >= 97 and c <= 102:
        return c - 87
    raise Error("journal: bad hex digit in \\u escape")


def append_utf8(mut out: List[UInt8], code: Int):
    if code < 0x80:
        out.append(UInt8(code))
    elif code < 0x800:
        out.append(UInt8(0xC0 | (code >> 6)))
        out.append(UInt8(0x80 | (code & 0x3F)))
    elif code < 0x10000:
        out.append(UInt8(0xE0 | (code >> 12)))
        out.append(UInt8(0x80 | ((code >> 6) & 0x3F)))
        out.append(UInt8(0x80 | (code & 0x3F)))
    else:
        out.append(UInt8(0xF0 | (code >> 18)))
        out.append(UInt8(0x80 | ((code >> 12) & 0x3F)))
        out.append(UInt8(0x80 | ((code >> 6) & 0x3F)))
        out.append(UInt8(0x80 | (code & 0x3F)))


struct Cursor(Movable):
    var data: List[UInt8]
    var pos: Int

    def __init__(out self, line: String):
        self.data = List[UInt8]()
        for b in line.as_bytes():
            self.data.append(b)
        self.pos = 0

    def at_end(self) -> Bool:
        return self.pos >= len(self.data)

    def byte(self, i: Int) -> Int:
        return Int(self.data[i])

    def peek(self) raises -> Int:
        if self.at_end():
            raise Error("journal: line ended early")
        return self.byte(self.pos)

    def take(mut self) raises -> Int:
        var c = self.peek()
        self.pos += 1
        return c

    def skip_space(mut self):
        while not self.at_end() and is_space(self.byte(self.pos)):
            self.pos += 1

    def expect(mut self, want: Int) raises:
        var got = self.take()
        if got != want:
            raise Error(
                "journal: unexpected byte "
                + String(got)
                + " at "
                + String(self.pos - 1)
            )

    def hex4(mut self) raises -> Int:
        var code = 0
        for _ in range(4):
            code = code * 16 + hex_value(self.take())
        return code

    def read_string(mut self) raises -> String:
        self.expect(QUOTE)
        var out = List[UInt8]()
        while True:
            var c = self.take()
            if c == QUOTE:
                break
            if c != BACKSLASH:
                out.append(UInt8(c))
                continue
            var e = self.take()
            if e == 117:  # u
                var code = self.hex4()
                # A surrogate pair is two \u escapes that make one code point.
                if (
                    code >= 0xD800
                    and code <= 0xDBFF
                    and self.pos + 1 < len(self.data)
                ):
                    if (
                        self.byte(self.pos) == BACKSLASH
                        and self.byte(self.pos + 1) == 117
                    ):
                        var save = self.pos
                        self.pos += 2
                        var low = self.hex4()
                        if low >= 0xDC00 and low <= 0xDFFF:
                            code = (
                                0x10000
                                + (code - 0xD800) * 1024
                                + (low - 0xDC00)
                            )
                        else:
                            self.pos = save
                append_utf8(out, code)
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
            else:  # \" \\ \/
                out.append(UInt8(e))
        return String(unsafe_from_utf8=out)

    def read_number(mut self) raises -> Float64:
        var negative = False
        if self.peek() == MINUS:
            self.pos += 1
            negative = True
        var mantissa = 0
        var decimals = 0
        var seen = False
        while (
            not self.at_end()
            and self.byte(self.pos) >= ZERO
            and self.byte(self.pos) <= NINE
        ):
            mantissa = mantissa * 10 + (self.byte(self.pos) - ZERO)
            self.pos += 1
            seen = True
        if not self.at_end() and self.byte(self.pos) == DOT:
            self.pos += 1
            while (
                not self.at_end()
                and self.byte(self.pos) >= ZERO
                and self.byte(self.pos) <= NINE
            ):
                mantissa = mantissa * 10 + (self.byte(self.pos) - ZERO)
                self.pos += 1
                decimals += 1
                seen = True
        if not seen:
            raise Error("journal: expected a number")
        if not self.at_end() and (
            self.byte(self.pos) == 101 or self.byte(self.pos) == 69
        ):
            raise Error("journal: exponents are not supported")
        # Dividing by an exact power of ten gives the correctly rounded value
        # for any mantissa below 2^53, which covers every journal number.
        var scale: Float64 = 1.0
        for _ in range(decimals):
            scale *= 10.0
        var value = Float64(mantissa) / scale
        if negative:
            value = -value
        return value

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
        # A number, true, false or null: read up to the next separator.
        while not self.at_end():
            var b = self.byte(self.pos)
            if b == COMMA or b == RBRACE or b == RBRACKET or is_space(b):
                break
            self.pos += 1


def parse_event(line: String) raises -> Event:
    var c = Cursor(line)
    var kind = String()
    var id = String()
    var text = String()
    var value: Float64 = 0.0
    var has_value = False
    c.skip_space()
    c.expect(LBRACE)
    while True:
        c.skip_space()
        var key = c.read_string()
        c.skip_space()
        c.expect(COLON)
        c.skip_space()
        if key == "kind":
            kind = c.read_string()
        elif key == "id":
            id = c.read_string()
        elif key == "text":
            text = c.read_string()
        elif key == "value":
            value = c.read_number()
            has_value = True
        else:
            c.skip_value()
        c.skip_space()
        var sep = c.take()
        if sep == RBRACE:
            break
        if sep != COMMA:
            raise Error("journal: expected , or } after a field")
    return Event(kind, id, text, value, has_value)


def read_events(path: String) raises -> List[Event]:
    var events = List[Event]()
    if not Path(path).exists():
        return events^
    var text = Path(path).read_text()
    for line in text.split("\n"):
        var trimmed = String(line).strip()
        if trimmed.byte_length() == 0:
            continue
        events.append(parse_event(String(trimmed)))
    return events^
