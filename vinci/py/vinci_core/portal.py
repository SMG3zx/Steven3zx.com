"""`vinci serve [port]`: the page of saved links on localhost.

Each request reads the journal again. The HTTP layer is Python's http.server (vinci_edge.portal_edge);
this file decides every answer. Requests are answered one at a time. A scan or a capture that is
still running is waited for, or read later.

Port of mojo/src/portal.mojo.
"""

from __future__ import annotations

import os
import subprocess  # noqa: F401  (the edge runs the browser; kept for parity)
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Optional

from .journal import Journal, directory_of
from .portal_assets import portal_placeholder
from .portal_html import title_of
from .portal_io import (
    absolute_path,
    clock,
    git_portal_files,
    listen,
    next_request,
    read_text_lenient,
    start_capture,
    start_probe,
)
from .portal_json import PortalEntry, portal_entries
from .portal_link import Link, preview_of, slug
from .portal_page import page
from .portal_url import byte_range, is_web_address, local_port_of, request_path
from .jsonline import ascii_lower
from .session import is_space, user_error
from .state import RETIRED, state_from

SCAN_EVERY = 30.0
RETRY_CAPTURE = 3600.0
DEFAULT_PORT = 4000
DEFAULT_CHROMIUM = "/usr/bin/chromium"


@dataclass
class Reply:
    """What one request gets: a status, "Name: value" header lines joined by newlines, and a body
    that is either text or the path of a file (file wins when set)."""

    status: int
    headers: str
    body: str
    file: str


@dataclass
class Context:
    """What the server remembers between requests: the scan, the captures, the addresses."""

    journal_file: str
    previews: str
    port: int
    chromium: str
    address_slugs: list[str] = field(default_factory=list)
    address_urls: list[str] = field(default_factory=list)
    tried_slugs: list[str] = field(default_factory=list)
    tried_at: list[float] = field(default_factory=list)
    has_scan: bool = False
    scan_at: float = 0.0
    scan_links: list[Link] = field(default_factory=list)
    has_probe: bool = False
    probe: Any = None


def split_nul(text: str) -> list[str]:
    return [piece for piece in text.split("\x00") if piece != ""]


def join_path(directory: str, name: str) -> str:
    """Path.Combine for a relative name."""
    if directory == "":
        return name
    if directory.endswith("/"):
        return directory + name
    return directory + "/" + name


def parent_directory(folder: str) -> Optional[str]:
    """Path.GetDirectoryName on Linux. None for the root and for an empty path."""
    if folder == "" or folder == "/":
        return None
    trimmed = folder
    while trimmed.endswith("/") and len(trimmed) > 1:
        trimmed = trimmed[:-1]
    cut = trimmed.rfind("/")
    if cut < 0:
        return ""
    if cut == 0:
        return "/"
    return trimmed[:cut]


def repository_of(folder: str) -> Optional[str]:
    """Tracker.RepositoryOf: the nearest folder at or above this one that holds a .git."""
    directory = folder
    while True:
        if Path(join_path(directory, ".git")).exists():
            return directory
        parent = parent_directory(directory)
        if parent is None:
            return None
        directory = parent


def _trim(text: str) -> str:
    """The text without leading and trailing white space (the Unicode White_Space set)."""
    cps = [ord(ch) for ch in text]
    lo = 0
    hi = len(cps)
    while lo < hi and is_space(cps[lo]):
        lo += 1
    while hi > lo and is_space(cps[hi - 1]):
        hi -= 1
    return "".join(chr(c) for c in cps[lo:hi])


def entry_link(entry: PortalEntry) -> Optional[Link]:
    """Parse's filter: a titled entry with an http or https address."""
    if entry.title is None or entry.url is None:
        return None
    title = _trim(entry.title)
    if title == "":
        return None
    url = entry.url
    if not is_web_address(url):
        return None
    return Link(title, url, entry.description, list(entry.tags), False)


def saved_links(journal_file: str) -> list[Link]:
    """The bookmarks in the journal, then the portal.json files tracked in each repository an
    idea lives in. The first link to a URL wins. cold-storage is never read."""
    state = state_from(Journal(journal_file).read())
    links: list[Link] = []
    for b in state.bookmarks:
        links.append(Link(b.title, b.url, b.why, list(b.tags), False))
    repositories: list[str] = []
    for idea in state.ideas:
        if idea.home is None or idea.stage == RETIRED:
            continue
        name = repository_of(idea.home)
        if name is None:
            continue
        if name not in repositories:
            repositories.append(name)
    files: list[str] = []
    for repo in repositories:
        for name in split_nul(git_portal_files(repo)):
            files.append(join_path(repo, name))
    files.sort(key=lambda f: f.encode("utf-8"))
    for file in files:
        parts = file.split("/")
        cold = "cold-storage" in parts
        if cold or not Path(file).is_file():
            continue
        for entry in portal_entries(read_text_lenient(file)):
            link = entry_link(entry)
            if link is not None:
                links.append(link)
    distinct: list[Link] = []
    seen: list[str] = []
    for link in links:
        if link.url not in seen:
            seen.append(link.url)
            distinct.append(link)
    return distinct


def listening_ports() -> list[int]:
    """Ports listening on loopback or any address, from /proc/net/tcp and tcp6."""
    loopback = ["0100007F", "00000000", "00000000000000000000000001000000", "00000000000000000000000000000000"]
    ports: list[int] = []
    for file in ["/proc/net/tcp", "/proc/net/tcp6"]:
        if not Path(file).is_file():
            continue
        text = Path(file).read_text()
        first = True
        for line in text.split("\n"):
            if first:
                first = False
                continue
            fields = [raw for raw in line.split(" ") if raw != ""]
            if len(fields) < 4 or fields[3] != "0A":
                continue
            local = fields[1]
            colon = local.rfind(":")
            if colon < 0:
                continue
            address = local[:colon]
            port_hex = local[colon + 1 :]
            if address not in loopback:
                continue
            value = 0
            for c in port_hex.encode("ascii"):
                if c <= 57:
                    digit = c - 48
                elif c <= 70:
                    digit = c - 55
                else:
                    digit = c - 87
                value = value * 16 + digit
            if value not in ports:
                ports.append(value)
    ports.sort()
    return ports


def scan_ports(own: int) -> list[int]:
    """The listening ports to ask. The portal's own port is left out: its answer is never listed
    (merge drops it), so asking it would only ask itself."""
    return [port for port in listening_ports() if port != own]


def discovered_links(hits: list) -> list[Link]:
    """One link for each answering port: its title, or localhost:<port>."""
    links: list[Link] = []
    for port, html in hits:
        port = int(port)
        title = title_of(str(html))
        shown = title if title is not None else "localhost:" + str(port)
        links.append(
            Link(shown, "http://localhost:" + str(port), "Found running on port " + str(port) + ".", ["local"], True)
        )
    return links


def consume_probe(ctx: Context) -> None:
    scan_at, hits = ctx.probe.collect()
    ctx.scan_at = float(scan_at)
    ctx.scan_links = discovered_links(hits)
    ctx.has_scan = True
    ctx.has_probe = False
    ctx.probe = None


def discovered(ctx: Context) -> list[Link]:
    """The discovered links: after the first scan, the last result is returned at once and a
    stale one is refreshed in the background, so a slow port never holds the page."""
    if ctx.has_probe and ctx.probe.done():
        consume_probe(ctx)
    stale = (not ctx.has_scan) or clock() - ctx.scan_at >= SCAN_EVERY
    if stale and not ctx.has_probe:
        ctx.probe = start_probe(scan_ports(ctx.port))
        ctx.has_probe = True
    if not ctx.has_scan:
        consume_probe(ctx)
    return list(ctx.scan_links)


def merge(saved: list[Link], found: list[Link], own_port: int) -> list[Link]:
    """The saved links, then each discovered link whose port no saved link or the portal itself
    already covers."""
    covered = [own_port]
    for link in saved:
        port = local_port_of(link.url)
        if port is not None:
            covered.append(port)
    out = list(saved)
    for link in found:
        port = local_port_of(link.url)
        if not (port is not None and port in covered):
            out.append(link)
    return out


def remember_address(ctx: Context, slug_name: str, url: str) -> None:
    if slug_name in ctx.address_slugs:
        ctx.address_urls[ctx.address_slugs.index(slug_name)] = url
        return
    ctx.address_slugs.append(slug_name)
    ctx.address_urls.append(url)


def capture(ctx: Context, slug_name: str, url: str, file: str) -> None:
    """Start a screenshot unless the browser is missing or this page was tried within the hour.
    The picture is taken in the background."""
    now = clock()
    if not Path(ctx.chromium).is_file():
        return
    for i in range(len(ctx.tried_slugs)):
        if ctx.tried_slugs[i] == slug_name and now - ctx.tried_at[i] < RETRY_CAPTURE:
            return
    stored = False
    for i in range(len(ctx.tried_slugs)):
        if ctx.tried_slugs[i] == slug_name:
            ctx.tried_at[i] = now
            stored = True
    if not stored:
        ctx.tried_slugs.append(slug_name)
        ctx.tried_at.append(now)
    draft = file[: len(file) - 4] + ".tmp.png"
    start_capture(url, draft, file, ctx.chromium)


def text_reply(status: int, headers: str, body: str) -> Reply:
    return Reply(status, headers, body, "")


def host_name(host: str) -> str:
    """The host of a Host header without its port: "127.0.0.1:4000" is 127.0.0.1."""
    colon = host.rfind(":")
    if colon < 0:
        return host
    return host[:colon]


def answer(ctx: Context, host: str, has_host: bool, target: str, loopback: str) -> Reply:
    """Answer one request.

    A socket is answered only for its own host name: 127.0.0.1 on the IPv4 socket, localhost on
    the IPv6 one. The port in Host is not checked. The 400 and 404 bodies are fixed text.
    """
    port_text = str(ctx.port)
    if not has_host or host == "":
        return text_reply(400, "Content-Type: text/html", "<h1>Bad Request (Invalid host name)</h1>")
    if host.startswith("[") or host.count(":") >= 2:
        # An IPv6 literal in Host is not read as a name: the URL is built from the port alone,
        # with a bracket when the host began with one.
        open_bracket = "[" if host.startswith("[") else ""
        return text_reply(
            400,
            "Content-Type: text/html",
            "<h1>Bad Request (Invalid url: http://" + open_bracket + ":" + port_text + target + ")</h1>",
        )
    if not target.startswith("/"):
        return text_reply(400, "Content-Type: text/html", "<h1>Bad Request (Invalid url: http://" + host + target + ")</h1>")
    name = ascii_lower(host_name(host))
    accepted = (loopback == "127.0.0.1" and name == "127.0.0.1") or (loopback == "::1" and name == "localhost")
    if not accepted:
        return text_reply(404, "Content-Type: text/html", "<h1>Not Found (Not Found)</h1>")

    path = request_path(target)
    if path == "/":
        saved = saved_links(ctx.journal_file)
        found = discovered(ctx)
        links = merge(saved, found, ctx.port)
        for link in links:
            remember_address(ctx, preview_of(link), link.url)
        return text_reply(200, "Content-Type: text/html; charset=utf-8", page(links))

    if path.startswith("/previews/") and path.endswith(".png"):
        inner = byte_range(path, 10, len(path) - 4)
        slug_name = slug(inner)
        file = join_path(ctx.previews, slug_name + ".png")
        if Path(file).is_file():
            return Reply(200, "Content-Type: image/png", "", file)
        for i in range(len(ctx.address_slugs)):
            if ctx.address_slugs[i] == slug_name:
                capture(ctx, slug_name, ctx.address_urls[i], file)
        return text_reply(200, "Cache-Control: no-store\nContent-Type: image/svg+xml", portal_placeholder())

    return text_reply(404, "Transfer-Encoding: chunked", "")


def send(req, reply: Reply) -> None:  # type: ignore[no-untyped-def]
    if reply.file != "":
        req.reply_file(reply.status, reply.headers, reply.file)
    else:
        req.reply_text(reply.status, reply.headers, reply.body)


def serve(port: int, journal_file: str) -> None:
    """Make the previews folder, listen on loopback, answer until stopped."""
    previews = join_path(directory_of(absolute_path(journal_file)), "previews")
    os.makedirs(previews, exist_ok=True)
    listener = listen(port)
    print("Portal is at http://127.0.0.1:" + str(port) + "/", flush=True)
    chromium = os.environ.get("CHROMIUM", DEFAULT_CHROMIUM)
    ctx = Context(journal_file, previews, port, chromium)
    while True:
        req = next_request(listener)
        try:
            reply = answer(ctx, req.host, req.has_host, req.target, req.loopback)
            send(req, reply)
        except Exception:
            # An answer that fails closes the request with an empty chunked 200.
            send(req, Reply(200, "Transfer-Encoding: chunked", "", ""))


def portal_command(args: list[str], journal_file: str) -> None:
    """`vinci serve [port]`. The port is digits only, as NumberStyles.None reads it."""
    port = DEFAULT_PORT
    if len(args) > 0:
        text = args[0]
        valid = 0 < len(text) <= 10 and all("0" <= ch <= "9" for ch in text)
        if not valid or int(text or "0") > 2147483647:
            raise user_error("<port> must be a number, got '" + text + "'")
        port = int(text)
    serve(port, journal_file)
