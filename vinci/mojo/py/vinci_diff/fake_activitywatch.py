"""A fake ActivityWatch server for the tracker's differential test (sync_diff).

It answers the one call the tracker makes, POST /api/0/query/, from scripted events. The
server's own part is done here, as aw-server-rust does it: window events are cut to the
periods the watcher was not away (filter_period_intersect), and afk events are returned as
they are when they overlap the period. Everything after that is the binary's own work: the
credit, the rounding, the journal lines.

Every query is kept in `requests`, so the harness can compare what each binary asked. The
server binds 127.0.0.1 on an ephemeral port, reads and writes nothing else, and is stopped
when the harness is done. `mode` makes it fail on purpose: "http500", "notarray", "badjson"
or "noafk" (the afk array missing).
"""

from __future__ import annotations

import json
import re
import socket
import threading
from dataclasses import dataclass, field
from datetime import UTC, datetime, timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Final, cast

NS: Final = 1_000_000_000
EPOCH: Final = datetime(1970, 1, 1, tzinfo=UTC)
STAMP: Final = re.compile(
    r"^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?(Z|[+-]\d{2}:\d{2})$"
)
MODES: Final = ("ok", "http500", "notarray", "badjson", "noafk")


@dataclass(frozen=True)
class Event:
    """One scripted event. ts is written as AW would write it (any offset form)."""

    ts: str
    seconds: float
    data: dict[str, str | None]


@dataclass(frozen=True)
class Span:
    """An event's time as nanoseconds since the epoch, with its duration."""

    start: int
    end: int
    raw: str
    data: dict[str, str | None]


def parse_ns(text: str) -> int:
    """The nanoseconds since the epoch of an ISO 8601 stamp, fraction digits kept to nine."""
    match = STAMP.match(text)
    if match is None:
        raise ValueError(f"not a stamp: {text}")
    year, month, day, hour, minute, second = (int(match.group(i)) for i in range(1, 7))
    fraction = (match.group(7) or "").ljust(9, "0")[:9]
    zone = match.group(8)
    offset = timedelta(0)
    if zone != "Z":
        sign = -1 if zone[0] == "-" else 1
        offset = sign * timedelta(hours=int(zone[1:3]), minutes=int(zone[4:6]))
    moment = datetime(year, month, day, hour, minute, second, tzinfo=UTC) - offset
    whole = moment - EPOCH
    seconds = whole.days * 86400 + whole.seconds
    return seconds * NS + int(fraction)


def stamp_ns(ns: int) -> str:
    """A nanosecond instant as AW's Z stamp, nine digits."""
    seconds, nanos = divmod(ns, NS)
    moment = EPOCH + timedelta(seconds=seconds)
    return moment.strftime("%Y-%m-%dT%H:%M:%S") + f".{nanos:09d}Z"


def _span(event: Event) -> Span:
    start = parse_ns(event.ts)
    return Span(start, start + round(event.seconds * NS), event.ts, event.data)


class _Handler(BaseHTTPRequestHandler):
    server_version = "fake-aw"
    protocol_version = "HTTP/1.1"

    def do_POST(self) -> None:
        owner = cast("FakeActivityWatch", self.server.owner)  # type: ignore[attr-defined]
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length)
        if self.path != "/api/0/query/":
            self._send(404, b"not found")
            return
        try:
            body = cast("dict[str, object]", json.loads(raw))
        except ValueError:
            self._send(400, b"bad body")
            return
        owner.requests.append(body)
        status, reply = owner.answer(body)
        self._send(status, reply)

    def _send(self, status: int, payload: bytes) -> None:
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, format: str, *args: object) -> None:
        return


@dataclass
class FakeActivityWatch:
    """The server. Set windows, afk and mode, then start(); requests holds every query."""

    windows: list[Event] = field(default_factory=lambda: list[Event]())
    afk: list[Event] = field(default_factory=lambda: list[Event]())
    mode: str = "ok"
    requests: list[dict[str, object]] = field(default_factory=lambda: list[dict[str, object]]())
    _server: ThreadingHTTPServer | None = None

    def start(self) -> str:
        """Listen on an ephemeral port and return the base URL."""
        httpd = ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
        httpd.owner = self  # type: ignore[attr-defined]
        threading.Thread(target=httpd.serve_forever, daemon=True).start()
        self._server = httpd
        return f"http://127.0.0.1:{httpd.server_address[1]}"

    def stop(self) -> None:
        if self._server is not None:
            self._server.shutdown()
            self._server.server_close()
            self._server = None

    def answer(self, body: dict[str, object]) -> tuple[int, bytes]:
        """The status and body the server would send for this query."""
        if self.mode == "http500":
            return (500, b'{"message":"boom"}')
        if self.mode == "badjson":
            return (200, b"<html>not json</html>")
        if self.mode == "notarray":
            return (200, b'{"result":1}')
        period = cast("list[str]", body["timeperiods"])[0].split("/")
        start, end = parse_ns(period[0]), parse_ns(period[1])
        afk_spans = [_span(e) for e in self.afk]
        not_afk = sorted(
            (s.start, s.end)
            for s in afk_spans
            if s.data.get("status") == "not-afk" and s.start < end and s.end > start
        )
        windows: list[dict[str, object]] = []
        for event in sorted(self.windows, key=lambda e: parse_ns(e.ts)):
            span = _span(event)
            for away_start, away_end in not_afk:
                lo = max(span.start, away_start, start)
                hi = min(span.end, away_end, end)
                if hi <= lo:
                    continue
                whole = lo == span.start and hi == span.end
                windows.append(
                    {
                        "timestamp": span.raw if whole else stamp_ns(lo),
                        "duration": event.seconds if whole else (hi - lo) / NS,
                        "data": dict(event.data),
                    }
                )
        afk = [
            {"timestamp": e.ts, "duration": e.seconds, "data": dict(e.data)}
            for e in self.afk
            if _span(e).start < end and _span(e).end > start
        ]
        result: dict[str, object] = {"window": windows, "afk": afk}
        if self.mode == "noafk":
            del result["afk"]
        return (200, json.dumps([result]).encode("utf-8"))


def closed_url() -> str:
    """A base URL on a port nothing listens on: the connection is refused."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
        probe.bind(("127.0.0.1", 0))
        port = probe.getsockname()[1]
    return f"http://127.0.0.1:{port}"
