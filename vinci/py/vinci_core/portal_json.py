"""The links in a portal.json: an array of { title, url, description?, tags? }.

Two passes. The first checks the whole text against the JSON grammar, with nesting up to 64
levels; a text that fails it gives no links. The second reads the entries. A property that appears
twice takes its last value. A lone surrogate escape in a string the portal reads fails the request
(see portal.py); it is an error here, with the marker below.

Port of mojo/src/portal_json.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional

MAX_JSON_DEPTH = 64
SURROGATE_MARKER = "portal:surrogate"


@dataclass
class PortalEntry:
    """One array element that is an object. Missing or non-string fields are None, tags empty."""

    title: Optional[str] = None
    url: Optional[str] = None
    description: Optional[str] = None
    tags: list[str] = field(default_factory=list)


def _is_hex_byte(c: int) -> bool:
    return (48 <= c <= 57) or (65 <= c <= 70) or (97 <= c <= 102)


def _hex_value(c: int) -> int:
    if c <= 57:
        return c - 48
    if c <= 70:
        return c - 55
    return c - 87


class JsonScan:
    def __init__(self, text: str) -> None:
        self.data: bytes = text.encode("utf-8")
        self.pos: int = 0

    def at_end(self) -> bool:
        return self.pos >= len(self.data)

    def peek(self) -> int:
        """The next byte, or -1 at the end."""
        if self.at_end():
            return -1
        return self.data[self.pos]

    def take(self) -> int:
        c = self.peek()
        if c < 0:
            raise ValueError("json: text ended early")
        self.pos += 1
        return c

    def skip_space(self) -> None:
        while not self.at_end() and self.peek() in (32, 9, 10, 13):
            self.pos += 1

    def expect(self, want: int) -> None:
        if self.take() != want:
            raise ValueError("json: unexpected byte")

    def validate_document(self) -> None:
        self.skip_space()
        self.validate_value(0)
        self.skip_space()
        if not self.at_end():
            raise ValueError("json: text after the value")

    def validate_value(self, depth: int) -> None:
        c = self.peek()
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
        elif c == 45 or (48 <= c <= 57):
            self.validate_number()
        else:
            raise ValueError("json: no value here")

    def literal(self, word: str) -> None:
        for b in word.encode("ascii"):
            if self.take() != b:
                raise ValueError("json: bad literal")

    def validate_object(self, depth: int) -> None:
        if depth > MAX_JSON_DEPTH:
            raise ValueError("json: nested too deeply")
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
            sep = self.take()
            if sep == 125:
                return
            if sep != 44:
                raise ValueError("json: expected , or } in an object")

    def validate_array(self, depth: int) -> None:
        if depth > MAX_JSON_DEPTH:
            raise ValueError("json: nested too deeply")
        self.expect(91)
        self.skip_space()
        if self.peek() == 93:
            self.pos += 1
            return
        while True:
            self.skip_space()
            self.validate_value(depth)
            self.skip_space()
            sep = self.take()
            if sep == 93:
                return
            if sep != 44:
                raise ValueError("json: expected , or ] in an array")

    def validate_string(self) -> None:
        """Checks the grammar only. A lone surrogate escape is allowed here, as in JsonDocument."""
        self.expect(34)
        while True:
            c = self.take()
            if c == 34:
                return
            if c < 32:
                raise ValueError("json: control character in a string")
            if c != 92:
                continue
            e = self.take()
            if e == 117:
                for _ in range(4):
                    if not _is_hex_byte(self.take()):
                        raise ValueError("json: bad \\u escape")
            elif e not in (34, 92, 47, 98, 102, 110, 114, 116):
                raise ValueError("json: bad escape")

    def validate_number(self) -> None:
        if self.peek() == 45:
            self.pos += 1
        c = self.peek()
        if c == 48:
            self.pos += 1
        elif 49 <= c <= 57:
            self.skip_digits()
        else:
            raise ValueError("json: bad number")
        if self.peek() == 46:
            self.pos += 1
            if not self.digit_follows():
                raise ValueError("json: bad fraction")
            self.skip_digits()
        if self.peek() in (101, 69):
            self.pos += 1
            if self.peek() in (43, 45):
                self.pos += 1
            if not self.digit_follows():
                raise ValueError("json: bad exponent")
            self.skip_digits()

    def digit_follows(self) -> bool:
        c = self.peek()
        return 48 <= c <= 57

    def skip_digits(self) -> None:
        while self.digit_follows():
            self.pos += 1

    def read_string(self) -> str:
        """A string with its escapes decoded. A lone surrogate raises SURROGATE_MARKER."""
        self.expect(34)
        out = bytearray()
        while True:
            c = self.take()
            if c == 34:
                break
            if c != 92:
                out.append(c)
                continue
            e = self.take()
            if e == 117:
                code = self.read_hex4()
                if 0xDC00 <= code <= 0xDFFF:
                    raise ValueError(SURROGATE_MARKER)
                if 0xD800 <= code <= 0xDBFF:
                    if self.take() != 92 or self.take() != 117:
                        raise ValueError(SURROGATE_MARKER)
                    low = self.read_hex4()
                    if low < 0xDC00 or low > 0xDFFF:
                        raise ValueError(SURROGATE_MARKER)
                    code = 0x10000 + (code - 0xD800) * 1024 + (low - 0xDC00)
                out += chr(code).encode("utf-8")
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
                out.append(e)
        return out.decode("utf-8")

    def read_hex4(self) -> int:
        code = 0
        for _ in range(4):
            c = self.take()
            if not _is_hex_byte(c):
                raise ValueError("json: bad \\u escape")
            code = code * 16 + _hex_value(c)
        return code

    def read_string_array(self) -> list[str]:
        out: list[str] = []
        self.expect(91)
        self.skip_space()
        if self.peek() == 93:
            self.pos += 1
            return out
        while True:
            self.skip_space()
            if self.peek() == 34:
                out.append(self.read_string())
            else:
                self.skip_value()
            self.skip_space()
            sep = self.take()
            if sep == 93:
                return out
            if sep != 44:
                raise ValueError("json: expected , or ] in an array")

    def skip_value(self) -> None:
        """Steps over one value that the validation pass has already checked."""
        self.validate_value(0)

    def read_entry(self) -> PortalEntry:
        """One object of the top-level array, with its last-occurrence rule applied."""
        entry = PortalEntry()
        self.expect(123)
        self.skip_space()
        if self.peek() == 125:
            self.pos += 1
            return entry
        while True:
            self.skip_space()
            key = self.read_string()
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
                    entry.tags = []
            else:
                self.skip_value()
            self.skip_space()
            sep = self.take()
            if sep == 125:
                return entry
            if sep != 44:
                raise ValueError("json: expected , or } in an object")

    def read_string_or_none(self) -> Optional[str]:
        if self.peek() == 34:
            return self.read_string()
        self.skip_value()
        return None


def portal_entries(text: str) -> list[PortalEntry]:
    """The entries of a portal.json. A text that is not JSON, or not an array, gives none.

    Raises SURROGATE_MARKER when a string the portal reads holds a lone surrogate escape: the
    request fails.
    """
    checker = JsonScan(text)
    try:
        checker.validate_document()
    except ValueError:
        return []
    scan = JsonScan(text)
    scan.skip_space()
    entries: list[PortalEntry] = []
    if scan.peek() != 91:
        return entries
    scan.pos += 1
    scan.skip_space()
    if scan.peek() == 93:
        return entries
    while True:
        scan.skip_space()
        if scan.peek() == 123:
            entries.append(scan.read_entry())
        else:
            scan.skip_value()
        scan.skip_space()
        sep = scan.take()
        if sep == 93:
            return entries
        if sep != 44:
            raise ValueError("json: expected , or ] in an array")
