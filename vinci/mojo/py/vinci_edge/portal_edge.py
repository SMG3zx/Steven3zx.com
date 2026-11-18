"""The network and process edge of `vinci serve`, called from Mojo (src/portal.mojo).

Mojo decides what every request gets. This module moves the bytes: it accepts requests on
127.0.0.1 and ::1 (what the C# HttpListener binds for `localhost`), hands each one to the
Mojo loop through a queue, and writes the reply. It also runs the work that waits on
something outside the process: probing local ports, reading a page for its title,
photographing a page with headless Chromium, listing tracked files with git.
"""

import http.client
import http.server
import os
import queue
import signal
import socket
import socketserver
import subprocess
import threading
import time
import urllib.parse
from collections.abc import Callable
from concurrent.futures import Future, ThreadPoolExecutor
from pathlib import Path
from typing import cast

# Probe.cs: a port is asked for its front page within this long, redirects included.
PROBE_PATIENCE = 1.5
# Capture: the page is fetched with a 15 second timeout, then Chromium gets 60 seconds.
FETCH_TIMEOUT = 15.0
CHROMIUM_PATIENCE = 60.0
# HttpClient follows up to 50 redirects by default.
MAX_REDIRECTS = 50
REDIRECT_STATUSES = frozenset({301, 302, 303, 307, 308})
POLL_SECONDS = 0.5
BODY_LIMIT = 1 << 20

# Code points where .NET's ToLowerInvariant differs from Python's str.lower(). Found by
# comparing both for every code point (portal_truth/lower_invariant.tsv is .NET's table).
# U+0130 keeps its dot in .NET; Python lowers it to two code points.
_LOWER_OVERRIDES: dict[int, int] = {
    0x130: 0x130,
    0xA7CE: 0xA7CF,
    0xA7D2: 0xA7D3,
    0xA7D4: 0xA7D5,
    **{0x16EA0 + i: 0x16EBB + i for i in range(25)},
}


def lower_invariant(text: str) -> str:
    """String.ToLowerInvariant, code point by code point."""
    out: list[str] = []
    for ch in text:
        override = _LOWER_OVERRIDES.get(ord(ch))
        if override is not None:
            out.append(chr(override))
            continue
        lowered = ch.lower()
        out.append(lowered if len(lowered) == 1 else ch)
    return "".join(out)


class Pending:
    """One request waiting for Mojo's answer. Its handler thread blocks until a reply_* call."""

    def __init__(
        self,
        method: str,
        target: str,
        host: str | None,
        loopback: str,
        keep_alive: bool,
    ) -> None:
        self.method = method
        self.target = target
        self.host = "" if host is None else host
        self.has_host = host is not None
        self.loopback = loopback
        self.keep_alive = keep_alive
        self.answered = threading.Event()
        self.status = 200
        self.headers: list[tuple[str, str]] = []
        self.body = b""
        self.chunked = False

    def reply_text(self, status: int, headers: str, text: str) -> None:
        self._finish(status, headers, text.encode("utf-8"))

    def reply_file(self, status: int, headers: str, path: str) -> None:
        self._finish(status, headers, Path(path).read_bytes())

    def _finish(self, status: int, headers: str, body: bytes) -> None:
        """Headers come as "Name: value" lines. Content-Length is computed here, not passed in."""
        self.status = status
        self.body = body
        for line in headers.split("\n"):
            if line == "":
                continue
            name, _, value = line.partition(": ")
            if name.lower() == "transfer-encoding" and value == "chunked":
                self.chunked = True
            else:
                self.headers.append((name, value))
        self.answered.set()


def _content_length(value: str | None) -> int:
    try:
        return max(0, int(value or "0"))
    except ValueError:
        return 0


class _Handler(http.server.BaseHTTPRequestHandler):
    """Reads each request the way the C# HttpListener does. Any method is answered alike."""

    protocol_version = "HTTP/1.1"
    # The base class sets these while parsing; they are kept here so that its logging and
    # error helpers always find them.
    requestline = ""
    request_version = "HTTP/1.1"
    command = ""
    path = ""

    def handle_one_request(self) -> None:
        self.close_connection = True
        line = self.rfile.readline(65537)
        if not line:
            return
        self.requestline = line.rstrip(b"\r\n").decode("latin-1")
        parts = line.rstrip(b"\r\n").split(b" ")
        if len(parts) != 3:
            self._send_error(400, "Bad Request (Invalid request line (parts).)", keep_alive=True)
            return
        method, target, version = parts
        self.command = method.decode("latin-1")
        self.path = target.decode("latin-1")
        self.request_version = version.decode("latin-1")
        if version not in (b"HTTP/1.1", b"HTTP/1.0"):
            self._send_error(
                505, "Http Version Not Supported (Http Version Not Supported)", keep_alive=False
            )
            return
        headers = http.client.parse_headers(self.rfile)
        self.headers = headers
        length = _content_length(headers.get("Content-Length"))
        if length > 0:
            _ = self.rfile.read(min(length, BODY_LIMIT))
        host = headers.get("Host")
        server = cast(_Server, self.server)
        pending = Pending(
            method.decode("latin-1"),
            target.decode("utf-8", errors="replace"),
            host,
            server.loopback,
            version == b"HTTP/1.0",
        )
        server.requests.put(pending)
        pending.answered.wait()
        try:
            self._write(pending)
        except OSError:
            # The client went away before the answer was written: nothing to send.
            return

    def _send_error(self, status: int, message: str, keep_alive: bool) -> None:
        """An error HttpListener writes itself, before any request reaches Mojo."""
        body = f"<h1>{message}</h1>".encode()
        self.send_response(status)
        self.send_header("Content-Type", "text/html")
        if keep_alive:
            self.send_header("Keep-Alive", "true")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)

    def _write(self, pending: Pending) -> None:
        self.send_response(pending.status)
        for name, value in pending.headers:
            self.send_header(name, value)
        if pending.keep_alive:
            self.send_header("Keep-Alive", "true")
        self.send_header("Connection", "close")
        if pending.chunked:
            self.send_header("Transfer-Encoding", "chunked")
            self.end_headers()
            if pending.method != "HEAD":
                self.wfile.write(b"0\r\n\r\n")
            return
        self.send_header("Content-Length", str(len(pending.body)))
        self.end_headers()
        if pending.method != "HEAD" and pending.body:
            self.wfile.write(pending.body)

    def log_request(self, code: object = "-", size: object = "-") -> None:
        """Requests are not logged, as the C# listener does not log them."""

    def log_message(self, format: str, *args: object) -> None:
        """Requests are not logged, as the C# listener does not log them."""


class _Server(http.server.ThreadingHTTPServer):
    """One loopback socket. HttpListener answers a socket only for the host name of its prefix:
    127.0.0.1 for the IPv4 socket and localhost for the IPv6 one (see portal.mojo)."""

    def __init__(
        self,
        address: tuple[str, int],
        requests: "queue.Queue[Pending]",
        family: socket.AddressFamily,
        loopback: str,
    ) -> None:
        self.requests = requests
        self.loopback = loopback
        self.address_family = family
        super().__init__(address, _Handler)

    def server_bind(self) -> None:
        """Bind without HTTPServer's name lookup: the portal is only ever on loopback."""
        socketserver.TCPServer.server_bind(self)
        host, port = self.server_address[0], self.server_address[1]
        self.server_name = str(host)
        self.server_port = int(port)


class Listener:
    """The loopback servers. Mojo takes requests from one queue, shared by both."""

    def __init__(self, port: int) -> None:
        self.queue: queue.Queue[Pending] = queue.Queue()
        servers = [_Server(("127.0.0.1", port), self.queue, socket.AF_INET, "127.0.0.1")]
        try:
            servers.append(_Server(("::1", port), self.queue, socket.AF_INET6, "::1"))
        except OSError:
            # No IPv6 loopback on this machine: IPv4 alone still serves the portal.
            pass
        for server in servers:
            threading.Thread(target=server.serve_forever, daemon=True).start()


def listen(port: int) -> Listener:
    """Bind 127.0.0.1:port and [::1]:port. Raises OSError when the port is taken."""
    return Listener(port)


def next_request(listener: Listener) -> Pending:
    """The next request, waiting for it. Ctrl-C ends the process as SIGINT does."""
    try:
        while True:
            try:
                return listener.queue.get(timeout=POLL_SECONDS)
            except queue.Empty:
                continue
    except KeyboardInterrupt:
        os._exit(128 + signal.SIGINT)


class Probe:
    """The front pages of local ports, asked for in parallel, as Portal.Scan does."""

    def __init__(self, ports: list[int]) -> None:
        self._lock = threading.Lock()
        self._waiting = len(ports)
        self._finished_at = time.monotonic()
        self._hits: dict[int, str] = {}
        self._all_done = threading.Event()
        self._pool = ThreadPoolExecutor(max_workers=max(1, len(ports)))
        if not ports:
            self._all_done.set()
        for port in ports:
            future = self._pool.submit(_probe_port, port)
            future.add_done_callback(self._recorder(port))
        self._pool.shutdown(wait=False)

    def _recorder(self, port: int) -> Callable[["Future[str | None]"], None]:
        def record(future: "Future[str | None]") -> None:
            text = future.result()
            with self._lock:
                if text is not None:
                    self._hits[port] = text
                self._waiting -= 1
                if self._waiting == 0:
                    self._finished_at = time.monotonic()
                    self._all_done.set()

        return record

    def done(self) -> bool:
        return self._all_done.is_set()

    def collect(self) -> tuple[float, list[tuple[int, str]]]:
        """When the last probe finished, and the pages that answered with HTML, by port."""
        self._all_done.wait()
        with self._lock:
            return self._finished_at, sorted(self._hits.items())


def start_probe(ports: list[int]) -> Probe:
    return Probe(ports)


class _Reply:
    def __init__(
        self, response: http.client.HTTPResponse, connection: http.client.HTTPConnection
    ) -> None:
        self.response = response
        self.connection = connection

    def close(self) -> None:
        self.response.close()
        self.connection.close()


def _follow(url: str, patience: float) -> _Reply | None:
    """GET a URL, following redirects, with `patience` seconds for the whole chain."""
    deadline = time.monotonic() + patience
    current = url
    for _ in range(MAX_REDIRECTS + 1):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return None
        try:
            parts = urllib.parse.urlsplit(current)
            if parts.scheme not in ("http", "https") or parts.hostname is None:
                return None
            connection: http.client.HTTPConnection
            if parts.scheme == "https":
                connection = http.client.HTTPSConnection(
                    parts.hostname, parts.port, timeout=remaining
                )
            else:
                connection = http.client.HTTPConnection(
                    parts.hostname, parts.port, timeout=remaining
                )
            target = parts.path or "/"
            if parts.query:
                target += "?" + parts.query
            connection.request("GET", target, headers={"Accept": "*/*"})
            response = connection.getresponse()
        except (OSError, ValueError, http.client.HTTPException):
            return None
        location = response.getheader("Location")
        if response.status in REDIRECT_STATUSES and location is not None:
            response.close()
            connection.close()
            current = urllib.parse.urljoin(current, location)
            continue
        return _Reply(response, connection)
    return None


def _media_and_charset(content_type: str | None) -> tuple[str, str]:
    """The media type as sent (before any parameter) and the charset parameter, if any."""
    if content_type is None:
        return "", ""
    media, _, params = content_type.partition(";")
    charset = ""
    for param in params.split(";"):
        name, _, value = param.partition("=")
        if name.strip().lower() == "charset":
            charset = value.strip().strip('"')
    return media.strip(), charset


def _decode(body: bytes, charset: str) -> str:
    """ReadAsStringAsync: the declared charset, else UTF-8, with a byte order mark removed."""
    try:
        text = body.decode(charset or "utf-8", errors="replace")
    except LookupError:
        text = body.decode("utf-8", errors="replace")
    return text.removeprefix("\ufeff")


def _probe_port(port: int) -> str | None:
    """A port is a web page when its front page answers 2xx with text/html."""
    got = _follow(f"http://127.0.0.1:{port}/", PROBE_PATIENCE)
    if got is None:
        return None
    try:
        response = got.response
        if not 200 <= response.status < 300:
            return None
        media, charset = _media_and_charset(response.getheader("Content-Type"))
        if media != "text/html":
            return None
        return _decode(response.read(), charset)
    except (OSError, ValueError, http.client.HTTPException):
        return None
    finally:
        got.close()


def read_text_lenient(path: str) -> str:
    """File.ReadAllText for a UTF-8 file: a byte order mark is dropped, bad bytes become U+FFFD."""
    text = Path(path).read_bytes().decode("utf-8", errors="replace")
    return text.removeprefix("\ufeff")


def git_portal_files(repository: str) -> str:
    """`git ls-files -z -- portal.json */portal.json` in repository, or "" when git fails."""
    try:
        done = subprocess.run(
            ["git", "-C", repository, "ls-files", "-z", "--", "portal.json", "*/portal.json"],
            capture_output=True,
            check=False,
        )
    except OSError:
        return ""
    if done.returncode != 0:
        return ""
    return done.stdout.decode("utf-8", errors="replace")


def clock() -> float:
    """A monotonic clock in seconds, for the scan and capture times."""
    return time.monotonic()


def absolute_path(path: str) -> str:
    """Path.GetFullPath: the path made absolute against the working directory."""
    return os.path.abspath(path)


def _status_of(url: str) -> int:
    got = _follow(url, FETCH_TIMEOUT)
    if got is None:
        return 0
    try:
        return got.response.status
    finally:
        got.close()


_capture_lock = threading.Lock()


def _capture(url: str, draft: str, final: str, chromium: str) -> None:
    """Photograph one page into `final`. One capture runs at a time; a failure leaves no picture."""
    with _capture_lock:
        try:
            if not 200 <= _status_of(url) < 300:
                return
            process = subprocess.Popen(
                [
                    chromium,
                    "--headless=new",
                    "--disable-gpu",
                    "--hide-scrollbars",
                    "--window-size=1200,750",
                    "--virtual-time-budget=8000",
                    f"--screenshot={draft}",
                    url,
                ],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                start_new_session=True,
            )
            try:
                process.wait(timeout=CHROMIUM_PATIENCE)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            if os.path.exists(draft):
                os.replace(draft, final)
        except (OSError, ValueError):
            # No browser, a bad address, or it could not be run: the placeholder stays.
            return


def start_capture(url: str, draft: str, final: str, chromium: str) -> None:
    """Take the picture in the background. The caller has checked that the browser exists."""
    threading.Thread(target=_capture, args=(url, draft, final, chromium), daemon=True).start()
