# The links in a portal.json: an array of { title, url, description?, tags? }, read the way
# src/Portal.cs Parse reads it with System.Text.Json.
#
# Two passes, as JsonDocument.Parse then the element reads work. The first checks the whole
# text against the JSON grammar, with nesting up to 64 levels; a text that fails it gives no
# links. The second reads the entries. A property that appears twice takes its last value.
# A lone surrogate escape in a string the portal reads makes .NET throw during the read, so
# that request fails (see portal.mojo); it is an error here too, with the marker below.

from text import utf8_append

comptime MAX_JSON_DEPTH = 64
comptime SURROGATE_MARKER = "portal:surrogate"


@fieldwise_init
struct PortalEntry(Copyable, Movable):
    """One array element that is an object. Missing or non-string fields are None, tags empty.
    """

    var title: Optional[String]
    var url: Optional[String]
    var description: Optional[String]
    var tags: List[String]


struct JsonScan(Movable):
    var data: List[UInt8]
    var pos: Int

    def __init__(out self, text: String):
        self.data = List[UInt8]()
        for b in text.as_bytes():
            self.data.append(b)
        self.pos = 0

    def at_end(self) -> Bool:
        return self.pos >= len(self.data)

    def peek(self) -> Int:
        """The next byte, or -1 at the end."""
        if self.at_end():
            return -1
        return Int(self.data[self.pos])

    def take(mut self) raises -> Int:
        var c = self.peek()
        if c < 0:
            raise Error("json: text ended early")
        self.pos += 1
        return c

    def skip_space(mut self):
        while not self.at_end():
            var c = self.peek()
            if c == 32 or c == 9 or c == 10 or c == 13:
                self.pos += 1
            else:
                break

    def expect(mut self, want: Int) raises:
        if self.take() != want:
            raise Error("json: unexpected byte")

    def validate_document(mut self) raises:
        self.skip_space()
        self.validate_value(0)
        self.skip_space()
        if not self.at_end():
            raise Error("json: text after the value")

    def validate_value(mut self, depth: Int) raises:
        var c = self.peek()
        if c == 34:
            self.validate_string()
        elif c == 123:
            self.validate_object(depth + 1)
        elif c == 91:
            self.validate_array(depth + 1)
        elif c == 116:
            self.literal("true")
        elif c == 102:
            self.literal("false")
        elif c == 110:
            self.literal("null")
        elif c == 45 or (c >= 48 and c <= 57):
            self.validate_number()
        else:
            raise Error("json: no value here")

    def literal(mut self, word: String) raises:
        for b in word.as_bytes():
            if self.take() != Int(b):
                raise Error("json: bad literal")

    def validate_object(mut self, depth: Int) raises:
        if depth > MAX_JSON_DEPTH:
            raise Error("json: nested too deeply")
        self.expect(123)
        self.skip_space()
        if self.peek() == 125:
            self.pos += 1
            return
        while True:
            self.skip_space()
            self.validate_string()
            self.skip_space()
            self.expect(58)
            self.skip_space()
            self.validate_value(depth)
            self.skip_space()
            var sep = self.take()
            if sep == 125:
                return
            if sep != 44:
                raise Error("json: expected , or } in an object")

    def validate_array(mut self, depth: Int) raises:
        if depth > MAX_JSON_DEPTH:
            raise Error("json: nested too deeply")
        self.expect(91)
        self.skip_space()
        if self.peek() == 93:
            self.pos += 1
            return
        while True:
            self.skip_space()
            self.validate_value(depth)
            self.skip_space()
            var sep = self.take()
            if sep == 93:
                return
            if sep != 44:
                raise Error("json: expected , or ] in an array")

    def validate_string(mut self) raises:
        """Checks the grammar only. A lone surrogate escape is allowed here, as in JsonDocument.
        """
        self.expect(34)
        while True:
            var c = self.take()
            if c == 34:
                return
            if c < 32:
                raise Error("json: control character in a string")
            if c != 92:
                continue
            var e = self.take()
            if e == 117:
                for _ in range(4):
                    if not _is_hex_byte(self.take()):
                        raise Error("json: bad \\u escape")
            elif not (
                e == 34
                or e == 92
                or e == 47
                or e == 98
                or e == 102
                or e == 110
                or e == 114
                or e == 116
            ):
                raise Error("json: bad escape")

    def validate_number(mut self) raises:
        if self.peek() == 45:
            self.pos += 1
        var c = self.peek()
        if c == 48:
            self.pos += 1
        elif c >= 49 and c <= 57:
            self.skip_digits()
        else:
            raise Error("json: bad number")
        if self.peek() == 46:
            self.pos += 1
            if not self.digit_follows():
                raise Error("json: bad fraction")
            self.skip_digits()
        if self.peek() == 101 or self.peek() == 69:
            self.pos += 1
            if self.peek() == 43 or self.peek() == 45:
                self.pos += 1
            if not self.digit_follows():
                raise Error("json: bad exponent")
            self.skip_digits()

    def digit_follows(self) -> Bool:
        var c = self.peek()
        return c >= 48 and c <= 57

    def skip_digits(mut self):
        while self.digit_follows():
            self.pos += 1

    def read_string(mut self) raises -> String:
        """A string with its escapes decoded. A lone surrogate raises SURROGATE_MARKER.
        """
        self.expect(34)
        var out = List[UInt8]()
        while True:
            var c = self.take()
            if c == 34:
                break
            if c != 92:
                out.append(UInt8(c))
                continue
            var e = self.take()
            if e == 117:
                var code = self.read_hex4()
                if code >= 0xDC00 and code <= 0xDFFF:
                    raise Error(SURROGATE_MARKER)
                if code >= 0xD800 and code <= 0xDBFF:
                    if self.take() != 92 or self.take() != 117:
                        raise Error(SURROGATE_MARKER)
                    var low = self.read_hex4()
                    if low < 0xDC00 or low > 0xDFFF:
                        raise Error(SURROGATE_MARKER)
                    code = 0x10000 + (code - 0xD800) * 1024 + (low - 0xDC00)
                utf8_append(out, code)
            elif e == 110:
                out.append(10)
            elif e == 116:
                out.append(9)
            elif e == 114:
                out.append(13)
            elif e == 98:
                out.append(8)
            elif e == 102:
                out.append(12)
            else:
                out.append(UInt8(e))
        return String(unsafe_from_utf8=out)

    def read_hex4(mut self) raises -> Int:
        var code = 0
        for _ in range(4):
            var c = self.take()
            if not _is_hex_byte(c):
                raise Error("json: bad \\u escape")
            code = code * 16 + _hex_value(c)
        return code

    def read_string_array(mut self) raises -> List[String]:
        var out = List[String]()
        self.expect(91)
        self.skip_space()
        if self.peek() == 93:
            self.pos += 1
            return out^
        while True:
            self.skip_space()
            if self.peek() == 34:
                out.append(self.read_string())
            else:
                self.skip_value()
            self.skip_space()
            var sep = self.take()
            if sep == 93:
                return out^
            if sep != 44:
                raise Error("json: expected , or ] in an array")

    def skip_value(mut self) raises:
        """Steps over one value that the validation pass has already checked."""
        self.validate_value(0)

    def read_entry(mut self) raises -> PortalEntry:
        """One object of the top-level array, with its last-occurrence rule applied.
        """
        var entry = PortalEntry(None, None, None, List[String]())
        self.expect(123)
        self.skip_space()
        if self.peek() == 125:
            self.pos += 1
            return entry^
        while True:
            self.skip_space()
            var key = self.read_string()
            self.skip_space()
            self.expect(58)
            self.skip_space()
            if key == "title":
                entry.title = self.read_string_or_none()
            elif key == "url":
                entry.url = self.read_string_or_none()
            elif key == "description":
                entry.description = self.read_string_or_none()
            elif key == "tags":
                if self.peek() == 91:
                    entry.tags = self.read_string_array()
                else:
                    self.skip_value()
                    entry.tags = List[String]()
            else:
                self.skip_value()
            self.skip_space()
            var sep = self.take()
            if sep == 125:
                return entry^
            if sep != 44:
                raise Error("json: expected , or } in an object")

    def read_string_or_none(mut self) raises -> Optional[String]:
        if self.peek() == 34:
            return Optional(self.read_string())
        self.skip_value()
        return None


def _is_hex_byte(c: Int) -> Bool:
    return (
        (c >= 48 and c <= 57) or (c >= 65 and c <= 70) or (c >= 97 and c <= 102)
    )


def _hex_value(c: Int) -> Int:
    if c <= 57:
        return c - 48
    if c <= 70:
        return c - 55
    return c - 87


def portal_entries(text: String) raises -> List[PortalEntry]:
    """The entries of a portal.json. A text that is not JSON, or not an array, gives none.

    Raises SURROGATE_MARKER when a string the portal reads holds a lone surrogate escape:
    .NET throws there, and the request fails.
    """
    var checker = JsonScan(text)
    try:
        checker.validate_document()
    except:
        return List[PortalEntry]()
    var scan = JsonScan(text)
    scan.skip_space()
    var entries = List[PortalEntry]()
    if scan.peek() != 91:
        return entries^
    scan.pos += 1
    scan.skip_space()
    if scan.peek() == 93:
        return entries^
    while True:
        scan.skip_space()
        if scan.peek() == 123:
            entries.append(scan.read_entry())
        else:
            scan.skip_value()
        scan.skip_space()
        var sep = scan.take()
        if sep == 93:
            return entries^
        if sep != 44:
            raise Error("json: expected , or ] in an array")
