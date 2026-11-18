# The append-only journal: one JSON object per line, the same file the C# vinci
# writes (src/Journal.cs). Everything vinci knows is a fold over these events.

from std.pathlib import Path
from std.python import Python, PythonObject

from embed_edge import edge_path
from jsonline import (
    Cursor,
    ascii_lower,
    format_json_double,
    json_instant,
    json_string,
)
from text import ascii_tail
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


def journal_lock_edge() raises -> PythonObject:
    """vinci_edge.journal_lock, with mojo/py on sys.path. Only the path is set up here:
    edge_module would also import the index's HTTP module, which the lock does not need.
    """
    edge_path()
    return Python.import_module("vinci_edge.journal_lock")


def edge_status(pair: PythonObject, what: String) raises:
    """Raises when the edge's status pair is not ok. The message is the edge's own text.
    """
    if String(pair[0]) != "ok":
        raise Error(what + String(pair[1]))


def next_id_of(events: List[Event], letter: String) raises -> String:
    """The next free id with a prefix letter (k1, e7 ...), from the ids in events.

    Callers mint under the lock: take the events from a JournalTx that has begun, and append
    the new line to the same transaction before it commits.
    """
    var best = 0
    for e in events:
        if e.id is None:
            continue
        var id = e.id.value()
        if id.startswith(letter) and id.byte_length() > 1:
            try:
                var n = Int(String(ascii_tail(id, 1)))
                if n > best:
                    best = n
            except:
                pass
    return letter + String(best + 1)


struct JournalTx(Movable):
    """One read-then-write transaction on the journal, under its append lock.

    begin() takes the lock; read() gives the journal's events plus what this transaction has
    not written yet; append() queues a line; flush() writes the queued lines with one fsync
    and keeps the lock; commit() flushes and releases; abort() drops the queue and releases.
    The lock is re-entrant in-process, so a read inside a transaction (and a nested
    transaction on the same file) does not wait for itself. Every raise between begin() and
    commit() should be followed by abort(); a process that exits drops the lock anyway.
    """

    var path: String
    var pending: List[Event]
    var held: Bool

    def __init__(out self, path: String):
        self.path = path
        self.pending = List[Event]()
        self.held = False

    def begin(mut self) raises:
        """Takes the lock. The journal's folder is created if it is missing."""
        if self.held:
            return
        var pair = journal_lock_edge().acquire(PythonObject(self.path))
        edge_status(pair, "")
        self.held = True

    def read(self) raises -> List[Event]:
        """The journal's events in file order, then the queued lines. Readers take no lock
        (Journal.read), so a read inside a transaction is the same as one outside it.
        """
        var events = Journal(self.path).read()
        for e in self.pending:
            events.append(e.copy())
        return events^

    def append(mut self, var e: Event) raises:
        """Queues one line. Nothing is written until flush or commit."""
        if not self.held:
            raise Error("journal: a line was queued outside a transaction")
        self.pending.append(e^)

    def flush(mut self) raises:
        """Writes the queued lines, in order, with one fsync. The lock stays held.
        """
        if len(self.pending) == 0:
            return
        if not self.held:
            raise Error("journal: lines were written outside a transaction")
        var text = String()
        for e in self.pending:
            text += serialize_event(e) + "\n"
        var pair = journal_lock_edge().append_durable(
            PythonObject(self.path), PythonObject(text)
        )
        edge_status(pair, "")
        self.pending = List[Event]()

    def commit(mut self) raises:
        """Flushes and releases the lock."""
        self.flush()
        self.release()

    def abort(mut self) raises:
        """Drops the queued lines and releases the lock. Safe to call twice."""
        self.pending = List[Event]()
        self.release()

    def release(mut self) raises:
        if not self.held:
            return
        self.held = False
        var pair = journal_lock_edge().release(PythonObject(self.path))
        edge_status(pair, "")


struct Journal(Movable):
    """The journal file at a path. Reads whole, appends one line at a time."""

    var path: String

    def __init__(out self, path: String):
        self.path = path

    def read(self) raises -> List[Event]:
        """Every event, in file order. A missing file is an empty journal, and nothing is
        created. Readers take no lock: a writer appends whole lines, so the only line a reader
        can see half written is the last one, which has no newline yet. It is left out here and
        read by the next call. A complete line that does not parse is still an error.
        """
        var events = List[Event]()
        if not Path(self.path).exists():
            return events^
        var bytes = Path(self.path).read_bytes()
        var last = -1
        for i in range(len(bytes)):
            if bytes[i] == 10:
                last = i
        var kept = List[UInt8]()
        for i in range(last + 1):
            kept.append(bytes[i])
        var text = String(unsafe_from_utf8=kept)
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

    def append(self, var e: Event) raises:
        """Append one line under the lock, with an fsync. For a read-then-append, use a
        JournalTx instead, so the id is minted under the same lock."""
        var tx = JournalTx(self.path)
        tx.begin()
        try:
            tx.append(e^)
            tx.commit()
        except err:
            tx.abort()
            raise err^
