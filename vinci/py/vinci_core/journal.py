"""The append-only journal: one JSON object per line.

Everything vinci knows is a fold over these events.

Port of mojo/src/journal.mojo. The Mojo version reached the lock and the durable append
through a Python edge; here they are imported directly.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, replace

from vinci_edge import journal_lock

from .jsonline import (
    Cursor,
    ascii_lower,
    format_json_double,
    json_instant,
    json_string,
)
from .timeutil import Instant, parse_instant


@dataclass
class Event:
    """One fact. Fields are written in this order.

    At and Kind are always present. The rest are optional, and an absent field is left out
    of the line.
    """

    at: Instant
    kind: str
    id: str | None = None
    text: str | None = None
    refs: list[str] | None = None
    value: float | None = None
    hi: float | None = None
    who: str | None = None
    why: str | None = None
    url: str | None = None
    metric: str | None = None
    tags: list[str] | None = None
    where_: str | None = None

    def copy(self) -> Event:
        return replace(
            self,
            refs=None if self.refs is None else list(self.refs),
            tags=None if self.tags is None else list(self.tags),
        )


def _optional_string_field(buf: list[str], name: str, value: str | None) -> None:
    if value is not None:
        buf.append("," + json_string(name) + ":" + json_string(value))


def _optional_list_field(buf: list[str], name: str, value: list[str] | None) -> None:
    if value is not None:
        buf.append("," + json_string(name) + ":[")
        buf.append(",".join(json_string(item) for item in value))
        buf.append("]")


def _optional_number_field(buf: list[str], name: str, value: float | None) -> None:
    if value is not None:
        buf.append("," + json_string(name) + ":" + format_json_double(value))


def serialize_event(e: Event) -> str:
    """One line of JSON, without the newline. Keys in declaration order, no spaces."""
    buf = ["{" + json_string("at") + ":" + json_instant(e.at)]
    buf.append("," + json_string("kind") + ":" + json_string(e.kind))
    _optional_string_field(buf, "id", e.id)
    _optional_string_field(buf, "text", e.text)
    _optional_list_field(buf, "refs", e.refs)
    _optional_number_field(buf, "value", e.value)
    _optional_number_field(buf, "hi", e.hi)
    _optional_string_field(buf, "who", e.who)
    _optional_string_field(buf, "why", e.why)
    _optional_string_field(buf, "url", e.url)
    _optional_string_field(buf, "metric", e.metric)
    _optional_list_field(buf, "tags", e.tags)
    _optional_string_field(buf, "where", e.where_)
    buf.append("}")
    return "".join(buf)


def _string_or_null(c: Cursor) -> str | None:
    if c.at_null():
        c.skip_null()
        return None
    return c.read_string()


def _number_or_null(c: Cursor) -> float | None:
    if c.at_null():
        c.skip_null()
        return None
    return c.read_number()


def _list_or_null(c: Cursor) -> list[str] | None:
    if c.at_null():
        c.skip_null()
        return None
    return c.read_string_array()


def parse_event(line: str) -> Event:
    """Parse one line. Field names match case-insensitively.

    Unknown fields are skipped whatever their value. A null is the same as an absent field.
    A missing or null at is an error, because DateTimeOffset cannot be null.
    """
    c = Cursor(line)
    at: Instant | None = None
    kind = ""
    fields: dict[str, object] = {}
    c.skip_space()
    c.expect(123)  # {
    c.skip_space()
    if c.peek() == 125:  # }
        c.take()
    else:
        while True:
            c.skip_space()
            key = ascii_lower(c.read_string())
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
                k = _string_or_null(c)
                kind = k if k is not None else ""
            elif key == "id":
                fields["id"] = _string_or_null(c)
            elif key == "text":
                fields["text"] = _string_or_null(c)
            elif key == "refs":
                fields["refs"] = _list_or_null(c)
            elif key == "value":
                fields["value"] = _number_or_null(c)
            elif key == "hi":
                fields["hi"] = _number_or_null(c)
            elif key == "who":
                fields["who"] = _string_or_null(c)
            elif key == "why":
                fields["why"] = _string_or_null(c)
            elif key == "url":
                fields["url"] = _string_or_null(c)
            elif key == "metric":
                fields["metric"] = _string_or_null(c)
            elif key == "tags":
                fields["tags"] = _list_or_null(c)
            elif key == "where":
                fields["where_"] = _string_or_null(c)
            else:
                c.skip_value()
            c.skip_space()
            sep = c.take()
            if sep == 125:  # }
                break
            if sep != 44:  # ,
                raise ValueError("journal: expected , or } after a field")
    c.skip_space()
    if not c.at_end():
        raise ValueError("journal: text after the closing brace")
    if at is None:
        raise ValueError("journal: event has no at")
    return Event(at, kind, **fields)  # type: ignore[arg-type]


def directory_of(path: str) -> str:
    """The part of a path before its last slash, or "" when there is none."""
    cut = path.rfind("/")
    if cut <= 0:
        return "/" if cut == 0 else ""
    return path[:cut]


def _edge_status(pair: tuple[str, str], what: str) -> None:
    """Raises when the edge's status pair is not ok. The message is the edge's own text."""
    if pair[0] != "ok":
        raise ValueError(what + pair[1])


def next_id_of(events: list[Event], letter: str) -> str:
    """The next free id with a prefix letter (k1, e7 ...), from the ids in events.

    Callers mint under the lock: take the events from a JournalTx that has begun, and append
    the new line to the same transaction before it commits.
    """
    best = 0
    for e in events:
        if e.id is None:
            continue
        ident = e.id
        if ident.startswith(letter) and len(ident) > 1:
            try:
                n = int(ident[1:])
                if n > best:
                    best = n
            except ValueError:
                pass
    return letter + str(best + 1)


class JournalTx:
    """One read-then-write transaction on the journal, under its append lock.

    begin() takes the lock; read() gives the journal's events plus what this transaction has
    not written yet; append() queues a line; flush() writes the queued lines with one fsync
    and keeps the lock; commit() flushes and releases; abort() drops the queue and releases.
    The lock is re-entrant in-process, so a read inside a transaction (and a nested
    transaction on the same file) does not wait for itself. Every raise between begin() and
    commit() should be followed by abort(); a process that exits drops the lock anyway.
    """

    def __init__(self, path: str) -> None:
        self.path: str = path
        self.pending: list[Event] = []
        self.held: bool = False

    def begin(self) -> None:
        """Takes the lock. The journal's folder is created if it is missing."""
        if self.held:
            return
        _edge_status(journal_lock.acquire(self.path), "")
        self.held = True

    def read(self) -> list[Event]:
        """The journal's events in file order, then the queued lines.

        Readers take no lock (Journal.read), so a read inside a transaction is the same as
        one outside it.
        """
        events = Journal(self.path).read()
        events.extend(e.copy() for e in self.pending)
        return events

    def append(self, e: Event) -> None:
        """Queues one line. Nothing is written until flush or commit."""
        if not self.held:
            raise ValueError("journal: a line was queued outside a transaction")
        self.pending.append(e)

    def flush(self) -> None:
        """Writes the queued lines, in order, with one fsync. The lock stays held."""
        if len(self.pending) == 0:
            return
        if not self.held:
            raise ValueError("journal: lines were written outside a transaction")
        text = "".join(serialize_event(e) + "\n" for e in self.pending)
        _edge_status(journal_lock.append_durable(self.path, text), "")
        self.pending = []

    def commit(self) -> None:
        """Flushes and releases the lock."""
        self.flush()
        self.release()

    def abort(self) -> None:
        """Drops the queued lines and releases the lock. Safe to call twice."""
        self.pending = []
        self.release()

    def release(self) -> None:
        if not self.held:
            return
        self.held = False
        _edge_status(journal_lock.release(self.path), "")


class Journal:
    """The journal file at a path. Reads whole, appends one line at a time."""

    def __init__(self, path: str) -> None:
        self.path: str = path

    def read(self) -> list[Event]:
        """Every event, in file order.

        A missing file is an empty journal, and nothing is created. Readers take no lock: a
        writer appends whole lines, so the only line a reader can see half written is the
        last one, which has no newline yet. It is left out here and read by the next call.
        A complete line that does not parse is still an error.
        """
        events: list[Event] = []
        if not os.path.exists(self.path):
            return events
        with open(self.path, "rb") as fh:
            data = fh.read()
        last = data.rfind(b"\n")
        kept = data[: last + 1]
        text = kept.decode("utf-8")
        for raw in text.split("\n"):
            line = raw
            if line.endswith("\r"):
                line = line[:-1]
            if len(line) == 0:
                continue
            events.append(parse_event(line))
        return events

    def append(self, e: Event) -> None:
        """Append one line under the lock, with an fsync.

        For a read-then-append, use a JournalTx instead, so the id is minted under the same
        lock.
        """
        tx = JournalTx(self.path)
        tx.begin()
        try:
            tx.append(e)
            tx.commit()
        except BaseException:
            tx.abort()
            raise


__all__ = [
    "Event",
    "Journal",
    "JournalTx",
    "directory_of",
    "next_id_of",
    "parse_event",
    "serialize_event",
]
