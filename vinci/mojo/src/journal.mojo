# The append-only journal: one JSON object per line, the same file the C# vinci
# writes (src/Journal.cs). Everything vinci knows is a fold over these events.

from std.pathlib import Path
from std.os import makedirs

from jsonline import (
    Cursor,
    ascii_lower,
    format_json_double,
    json_instant,
    json_string,
)
from timeutil import Instant, ascii_prefix, parse_instant


struct Event(Copyable, Movable):
    """One fact. Fields follow Event in src/Journal.cs, in the same order.

    At and Kind are always present. The rest are optional, and an absent field
    is left out of the line, as the C# serializer does with WhenWritingNull.
    """

    var at: Instant
    var kind: String
    var id: Optional[String]
    var text: Optional[String]
    var refs: Optional[List[String]]
    var value: Optional[Float64]
    var hi: Optional[Float64]
    var who: Optional[String]
    var why: Optional[String]
    var url: Optional[String]
    var metric: Optional[String]
    var tags: Optional[List[String]]
    var where_: Optional[String]

    def __init__(
        out self,
        var at: Instant,
        var kind: String,
        var id: Optional[String] = None,
        var text: Optional[String] = None,
        var refs: Optional[List[String]] = None,
        var value: Optional[Float64] = None,
        var hi: Optional[Float64] = None,
        var who: Optional[String] = None,
        var why: Optional[String] = None,
        var url: Optional[String] = None,
        var metric: Optional[String] = None,
        var tags: Optional[List[String]] = None,
        var where_: Optional[String] = None,
    ):
        self.at = at^
        self.kind = kind^
        self.id = id^
        self.text = text^
        self.refs = refs^
        self.value = value^
        self.hi = hi^
        self.who = who^
        self.why = why^
        self.url = url^
        self.metric = metric^
        self.tags = tags^
        self.where_ = where_^


def optional_string_field(
    mut buf: String, name: String, value: Optional[String]
) raises:
    if value is not None:
        buf += "," + json_string(name) + ":" + json_string(value.value())


def optional_list_field(
    mut buf: String, name: String, value: Optional[List[String]]
) raises:
    if value is not None:
        buf += "," + json_string(name) + ":["
        var first = True
        for item in value.value():
            if not first:
                buf += ","
            buf += json_string(item)
            first = False
        buf += "]"


def optional_number_field(
    mut buf: String, name: String, value: Optional[Float64]
) raises:
    if value is not None:
        buf += "," + json_string(name) + ":" + format_json_double(value.value())


def serialize_event(e: Event) raises -> String:
    """One line of JSON, without the newline. Keys in declaration order, no spaces.
    """
    var buf = "{" + json_string("at") + ":" + json_instant(e.at)
    buf += "," + json_string("kind") + ":" + json_string(e.kind)
    optional_string_field(buf, "id", e.id)
    optional_string_field(buf, "text", e.text)
    optional_list_field(buf, "refs", e.refs)
    optional_number_field(buf, "value", e.value)
    optional_number_field(buf, "hi", e.hi)
    optional_string_field(buf, "who", e.who)
    optional_string_field(buf, "why", e.why)
    optional_string_field(buf, "url", e.url)
    optional_string_field(buf, "metric", e.metric)
    optional_list_field(buf, "tags", e.tags)
    optional_string_field(buf, "where", e.where_)
    return buf + "}"


def read_string_or_null(mut c: Cursor) raises -> Optional[String]:
    if c.at_null():
        c.skip_null()
        return None
    return c.read_string()


def read_number_or_null(mut c: Cursor) raises -> Optional[Float64]:
    if c.at_null():
        c.skip_null()
        return None
    return c.read_number()


def read_list_or_null(mut c: Cursor) raises -> Optional[List[String]]:
    if c.at_null():
        c.skip_null()
        return None
    return c.read_string_array()


def parse_event(line: String) raises -> Event:
    """Parse one line. Field names match case-insensitively, as the C# reader does.

    Unknown fields are skipped whatever their value. A null is the same as an
    absent field. A missing or null at is an error, because DateTimeOffset cannot
    be null.
    """
    var c = Cursor(line)
    var at: Optional[Instant] = None
    var kind = String()
    var id: Optional[String] = None
    var text: Optional[String] = None
    var refs: Optional[List[String]] = None
    var value: Optional[Float64] = None
    var hi: Optional[Float64] = None
    var who: Optional[String] = None
    var why: Optional[String] = None
    var url: Optional[String] = None
    var metric: Optional[String] = None
    var tags: Optional[List[String]] = None
    var where_: Optional[String] = None
    c.skip_space()
    c.expect(123)  # {
    c.skip_space()
    if c.peek() == 125:  # }
        _ = c.take()
    else:
        while True:
            c.skip_space()
            var key = ascii_lower(c.read_string())
            c.skip_space()
            c.expect(58)  # :
            c.skip_space()
            if key == "at":
                if c.at_null():
                    c.skip_null()
                    at = None
                else:
                    at = parse_instant(c.read_string())
            elif key == "kind":
                var k = read_string_or_null(c)
                kind = k.value() if k is not None else String()
            elif key == "id":
                id = read_string_or_null(c)
            elif key == "text":
                text = read_string_or_null(c)
            elif key == "refs":
                refs = read_list_or_null(c)
            elif key == "value":
                value = read_number_or_null(c)
            elif key == "hi":
                hi = read_number_or_null(c)
            elif key == "who":
                who = read_string_or_null(c)
            elif key == "why":
                why = read_string_or_null(c)
            elif key == "url":
                url = read_string_or_null(c)
            elif key == "metric":
                metric = read_string_or_null(c)
            elif key == "tags":
                tags = read_list_or_null(c)
            elif key == "where":
                where_ = read_string_or_null(c)
            else:
                c.skip_value()
            c.skip_space()
            var sep = c.take()
            if sep == 125:  # }
                break
            if sep != 44:  # ,
                raise Error("journal: expected , or } after a field")
    c.skip_space()
    if not c.at_end():
        raise Error("journal: text after the closing brace")
    if at is None:
        raise Error("journal: event has no at")
    return Event(
        at.value().copy(),
        kind^,
        id^,
        text^,
        refs^,
        value^,
        hi^,
        who^,
        why^,
        url^,
        metric^,
        tags^,
        where_^,
    )


def directory_of(path: String) -> String:
    """The part of a path before its last slash, or "" when there is none."""
    var src = path.as_bytes()
    var cut = -1
    for i in range(len(src)):
        if src[i] == 47:  # /
            cut = i
    if cut <= 0:
        return "/" if cut == 0 else ""
    var out = List[UInt8]()
    for i in range(cut):
        out.append(src[i])
    return String(unsafe_from_utf8=out)


struct Journal(Movable):
    """The journal file at a path. Reads whole, appends one line at a time."""

    var path: String

    def __init__(out self, path: String):
        self.path = path

    def read(self) raises -> List[Event]:
        """Every event, in file order. A missing file is an empty journal."""
        var events = List[Event]()
        if not Path(self.path).exists():
            return events^
        var text = Path(self.path).read_text()
        for raw in text.split("\n"):
            var line = String(raw)
            if (
                line.byte_length() > 0
                and line.as_bytes()[line.byte_length() - 1] == 13
            ):
                line = ascii_prefix(line, line.byte_length() - 1)
            if line.byte_length() == 0:
                continue
            events.append(parse_event(line))
        return events^

    def append(self, e: Event) raises:
        """Append one line, creating the parent directories first."""
        var dir = directory_of(self.path)
        if dir.byte_length() > 0:
            makedirs(dir, exist_ok=True)
        with open(self.path, "a") as f:
            f.write(serialize_event(e) + "\n")
