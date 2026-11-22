# `vinci serve [port]`: the page of saved links on localhost.
#
# Each request reads the journal again. The HTTP layer is Python's http.server
# (portal_io.mojo, portal_edge.py); this file decides every answer.
# Requests are answered one at a time. A scan or a capture that is still running is
# waited for, or read later.

from std.os import makedirs
from std.pathlib import Path
from std.python import Python, PythonObject

from journal import Journal, directory_of
from portal_html import title_of
from portal_io import (
    absolute_path,
    clock,
    git_portal_files,
    listen,
    next_request,
    read_text_lenient,
    start_capture,
    start_probe,
    env_or,
)
from portal_json import PortalEntry, SURROGATE_MARKER, portal_entries
from portal_link import Link, preview_of, slug
from portal_page import page
from portal_assets import portal_placeholder
from jsonline import ascii_lower as lower_ascii
from portal_url import byte_range, is_web_address, local_port_of, request_path
from session import is_space, user_error
from state import RETIRED, state_from
from text import code_points_to_string, utf8_code_points

comptime SCAN_EVERY = 30.0
comptime RETRY_CAPTURE = 3600.0
comptime DEFAULT_PORT = 4000
comptime DEFAULT_CHROMIUM = "/usr/bin/chromium"


@fieldwise_init
struct Reply(Copyable, Movable):
    """What one request gets: a status, "Name: value" header lines joined by newlines, and a
    body that is either text or the path of a file (file wins when set)."""

    var status: Int
    var headers: String
    var body: String
    var file: String


struct Context(Movable):
    """What the server remembers between requests: the scan, the captures, the addresses.
    """

    var journal_file: String
    var previews: String
    var port: Int
    var chromium: String
    var address_slugs: List[String]
    var address_urls: List[String]
    var tried_slugs: List[String]
    var tried_at: List[Float64]
    var has_scan: Bool
    var scan_at: Float64
    var scan_links: List[Link]
    var has_probe: Bool
    var probe: PythonObject

    def __init__(
        out self,
        var journal_file: String,
        var previews: String,
        port: Int,
        var chromium: String,
    ) raises:
        self.journal_file = journal_file^
        self.previews = previews^
        self.port = port
        self.chromium = chromium^
        self.address_slugs = List[String]()
        self.address_urls = List[String]()
        self.tried_slugs = List[String]()
        self.tried_at = List[Float64]()
        self.has_scan = False
        self.scan_at = 0.0
        self.scan_links = List[Link]()
        self.has_probe = False
        self.probe = Python.none()


def is_true(value: PythonObject) raises -> Bool:
    return String(value) == "True"


def split_path_segments(path: String) -> List[String]:
    var out = List[String]()
    var b = path.as_bytes()
    var start = 0
    for i in range(len(b) + 1):
        if i == len(b) or b[i] == 47:
            var piece = List[UInt8]()
            for k in range(start, i):
                piece.append(b[k])
            out.append(String(unsafe_from_utf8=piece))
            start = i + 1
    return out^


def join_path(dir: String, name: String) -> String:
    """Path.Combine for a relative name."""
    if dir.byte_length() == 0:
        return name
    if dir.endswith("/"):
        return dir + name
    return dir + "/" + name


def parent_directory(folder: String) -> Optional[String]:
    """Path.GetDirectoryName on Linux. None for the root and for an empty path.
    """
    if folder.byte_length() == 0 or folder == "/":
        return None
    var trimmed = folder
    while trimmed.endswith("/") and trimmed.byte_length() > 1:
        trimmed = byte_range(trimmed, 0, trimmed.byte_length() - 1)
    var b = trimmed.as_bytes()
    var cut = -1
    for i in range(len(b)):
        if b[i] == 47:
            cut = i
    if cut < 0:
        return String("")
    if cut == 0:
        return String("/")
    return byte_range(trimmed, 0, cut)


def repository_of(folder: String) raises -> Optional[String]:
    """Tracker.RepositoryOf: the nearest folder at or above this one that holds a .git.
    """
    var dir = folder
    while True:
        if Path(join_path(dir, ".git")).exists():
            return dir
        var parent = parent_directory(dir)
        if parent is None:
            return None
        dir = parent.value()


def split_nul(text: String) -> List[String]:
    var out = List[String]()
    var b = text.as_bytes()
    var start = 0
    for i in range(len(b) + 1):
        if i == len(b) or b[i] == 0:
            if i > start:
                var piece = List[UInt8]()
                for k in range(start, i):
                    piece.append(b[k])
                out.append(String(unsafe_from_utf8=piece))
            start = i + 1
    return out^


def sort_ordinal(mut items: List[String]):
    """Ordinal order by bytes, which matches UTF-16 order for every path the portal sees.
    """
    for i in range(1, len(items)):
        var j = i
        while j > 0 and items[j] < items[j - 1]:
            var swap = items[j].copy()
            items[j] = items[j - 1].copy()
            items[j - 1] = swap^
            j -= 1


def entry_link(entry: PortalEntry) raises -> Optional[Link]:
    """Parse's filter: a titled entry with an http or https address."""
    if entry.title is None or entry.url is None:
        return None
    var title = _trim(entry.title.value())
    if title.byte_length() == 0:
        return None
    var url = entry.url.value()
    if not is_web_address(url):
        return None
    return Optional(
        Link(title, url, entry.description.copy(), entry.tags.copy(), False)
    )


def _trim(text: String) raises -> String:
    var cps = utf8_code_points(text)
    var lo = 0
    var hi = len(cps)
    while lo < hi and is_space(cps[lo]):
        lo += 1
    while hi > lo and is_space(cps[hi - 1]):
        hi -= 1
    var kept = List[Int]()
    for i in range(lo, hi):
        kept.append(cps[i])
    return code_points_to_string(kept)


def saved_links(journal_file: String) raises -> List[Link]:
    """The bookmarks in the journal, then the portal.json files tracked in each
    repository an idea lives in. The first link to a URL wins. cold-storage is never read.
    """
    var state = state_from(Journal(journal_file).read())
    var links = List[Link]()
    for b in state.bookmarks:
        links.append(
            Link(
                b.title.copy(), b.url.copy(), b.why.copy(), b.tags.copy(), False
            )
        )
    var repositories = List[String]()
    for idea in state.ideas:
        if idea.home is None or idea.stage == RETIRED:
            continue
        var repo = repository_of(idea.home.value())
        if repo is None:
            continue
        var name = repo.value()
        var known = False
        for have in repositories:
            if have == name:
                known = True
        if not known:
            repositories.append(name)
    var files = List[String]()
    for repo in repositories:
        for name in split_nul(git_portal_files(repo)):
            files.append(join_path(repo, name))
    sort_ordinal(files)
    for file in files:
        var parts = split_path_segments(file)
        var cold = False
        for part in parts:
            if part == "cold-storage":
                cold = True
        if cold or not Path(file).is_file():
            continue
        for entry in portal_entries(read_text_lenient(file)):
            var link = entry_link(entry)
            if link:
                links.append(link.value().copy())
    var distinct = List[Link]()
    var seen = List[String]()
    for link in links:
        var known = False
        for url in seen:
            if url == link.url:
                known = True
                break
        if not known:
            seen.append(link.url)
            distinct.append(link.copy())
    return distinct^


def listening_ports() raises -> List[Int]:
    """Ports listening on loopback or any address, from /proc/net/tcp and tcp6.
    """
    var loopback = List[String]()
    loopback.append("0100007F")
    loopback.append("00000000")
    loopback.append("00000000000000000000000001000000")
    loopback.append("00000000000000000000000000000000")
    var ports = List[Int]()
    for file in ["/proc/net/tcp", "/proc/net/tcp6"]:
        if not Path(file).is_file():
            continue
        var text = Path(file).read_text()
        var first = True
        for line in text.split("\n"):
            if first:
                first = False
                continue
            var fields = List[String]()
            for raw in String(line).split(" "):
                if String(raw).byte_length() > 0:
                    fields.append(String(raw))
            if len(fields) < 4 or fields[3] != "0A":
                continue
            var colon = -1
            var local = fields[1].as_bytes()
            for i in range(len(local)):
                if local[i] == 58:
                    colon = i
            if colon < 0:
                continue
            var address = byte_range(fields[1], 0, colon)
            var port_hex = byte_range(
                fields[1], colon + 1, fields[1].byte_length()
            )
            var known = False
            for candidate in loopback:
                if candidate == address:
                    known = True
            if not known:
                continue
            var value = 0
            for b in port_hex.as_bytes():
                var c = Int(b)
                var digit = c - 48 if c <= 57 else (
                    c - 55 if c <= 70 else c - 87
                )
                value = value * 16 + digit
            var present = False
            for have in ports:
                if have == value:
                    present = True
            if not present:
                ports.append(value)
    ports_sorted(ports)
    return ports^


def ports_sorted(mut ports: List[Int]):
    for i in range(1, len(ports)):
        var j = i
        while j > 0 and ports[j] < ports[j - 1]:
            var swap = ports[j]
            ports[j] = ports[j - 1]
            ports[j - 1] = swap
            j -= 1


def scan_ports(own: Int) raises -> List[Int]:
    """The listening ports to ask. The portal's own port is left out: its answer is never
    listed (Merge drops it), so asking it would only ask itself."""
    var out = List[Int]()
    for port in listening_ports():
        if port != own:
            out.append(port)
    return out^


def discovered_links(hits: PythonObject) raises -> List[Link]:
    """One link for each answering port: its title, or localhost:<port>."""
    var links = List[Link]()
    for hit in hits:
        var port = Int(String(hit[0]))
        var html = String(hit[1])
        var title = title_of(html)
        var shown = title.value() if title else "localhost:" + String(port)
        var description = Optional(
            "Found running on port " + String(port) + "."
        )
        var tags = List[String]()
        tags.append("local")
        links.append(
            Link(
                shown,
                "http://localhost:" + String(port),
                description^,
                tags^,
                True,
            )
        )
    return links^


def consume_probe(mut ctx: Context) raises:
    var pair = ctx.probe.collect()
    ctx.scan_at = Float64(String(pair[0]))
    ctx.scan_links = discovered_links(pair[1])
    ctx.has_scan = True
    ctx.has_probe = False
    ctx.probe = Python.none()


def discovered(mut ctx: Context) raises -> List[Link]:
    """The discovered links: after the first scan, the last result is returned at once and a
    stale one is refreshed in the background, so a slow port never holds the page.
    """
    if ctx.has_probe and is_true(ctx.probe.done()):
        consume_probe(ctx)
    var stale = not ctx.has_scan or clock() - ctx.scan_at >= SCAN_EVERY
    if stale and not ctx.has_probe:
        ctx.probe = start_probe(scan_ports(ctx.port))
        ctx.has_probe = True
    if not ctx.has_scan:
        consume_probe(ctx)
    return ctx.scan_links.copy()


def merge(
    saved: List[Link], found: List[Link], own_port: Int
) raises -> List[Link]:
    """The saved links, then each discovered link whose port no saved link or
    the portal itself already covers."""
    var covered = List[Int]()
    covered.append(own_port)
    for link in saved:
        var port = local_port_of(link.url)
        if port:
            covered.append(port.value())
    var out = saved.copy()
    for link in found:
        var port = local_port_of(link.url)
        var known = False
        for c in covered:
            if port and c == port.value():
                known = True
        if not known:
            out.append(link.copy())
    return out^


def remember_address(mut ctx: Context, slug_name: String, url: String):
    for i in range(len(ctx.address_slugs)):
        if ctx.address_slugs[i] == slug_name:
            ctx.address_urls[i] = url
            return
    ctx.address_slugs.append(slug_name)
    ctx.address_urls.append(url)


def capture(
    mut ctx: Context, slug_name: String, url: String, file: String
) raises:
    """Start a screenshot unless the browser is missing or this page was
    tried within the hour. The picture is taken in the background."""
    var now = clock()
    if not Path(ctx.chromium).is_file():
        return
    for i in range(len(ctx.tried_slugs)):
        if (
            ctx.tried_slugs[i] == slug_name
            and now - ctx.tried_at[i] < RETRY_CAPTURE
        ):
            return
    var stored = False
    for i in range(len(ctx.tried_slugs)):
        if ctx.tried_slugs[i] == slug_name:
            ctx.tried_at[i] = now
            stored = True
    if not stored:
        ctx.tried_slugs.append(slug_name)
        ctx.tried_at.append(now)
    var draft = byte_range(file, 0, file.byte_length() - 4) + ".tmp.png"
    start_capture(url, draft, file, ctx.chromium)


def text_reply(status: Int, headers: String, body: String) -> Reply:
    return Reply(status, headers, body, String(""))


def host_name(host: String) -> String:
    """The host of a Host header without its port: "127.0.0.1:4000" is 127.0.0.1.
    """
    var b = host.as_bytes()
    var colon = -1
    for i in range(len(b)):
        if b[i] == 58:
            colon = i
    if colon < 0:
        return host
    return byte_range(host, 0, colon)


def answer(
    mut ctx: Context,
    host: String,
    has_host: Bool,
    target: String,
    loopback: String,
) raises -> Reply:
    """Answer one request.

    A socket is answered only for its own host name: 127.0.0.1 on the IPv4 socket,
    localhost on the IPv6 one. The port in Host is not checked. The 400 and 404 bodies
    are fixed text.
    """
    var port_text = String(ctx.port)
    if not has_host or host.byte_length() == 0:
        return text_reply(
            400,
            "Content-Type: text/html",
            "<h1>Bad Request (Invalid host name)</h1>",
        )
    if host.startswith("[") or host.count(":") >= 2:
        # An IPv6 literal in Host is not read as a name: the URL is built from the
        # port alone, with a bracket when the host began with one.
        var open_bracket = "[" if host.startswith("[") else ""
        return text_reply(
            400,
            "Content-Type: text/html",
            "<h1>Bad Request (Invalid url: http://"
            + open_bracket
            + ":"
            + port_text
            + target
            + ")</h1>",
        )
    if not target.startswith("/"):
        return text_reply(
            400,
            "Content-Type: text/html",
            "<h1>Bad Request (Invalid url: http://" + host + target + ")</h1>",
        )
    var name = lower_ascii(host_name(host))
    var accepted = (loopback == "127.0.0.1" and name == "127.0.0.1") or (
        loopback == "::1" and name == "localhost"
    )
    if not accepted:
        return text_reply(
            404, "Content-Type: text/html", "<h1>Not Found (Not Found)</h1>"
        )

    var path = request_path(target)
    if path == "/":
        var saved = saved_links(ctx.journal_file)
        var found = discovered(ctx)
        var links = merge(saved, found, ctx.port)
        for link in links:
            remember_address(ctx, preview_of(link), link.url)
        return text_reply(
            200, "Content-Type: text/html; charset=utf-8", page(links)
        )

    if path.startswith("/previews/") and path.endswith(".png"):
        var inner = byte_range(path, 10, path.byte_length() - 4)
        var slug_name = slug(inner)
        var file = join_path(ctx.previews, slug_name + ".png")
        if Path(file).is_file():
            return Reply(200, "Content-Type: image/png", String(""), file)
        for i in range(len(ctx.address_slugs)):
            if ctx.address_slugs[i] == slug_name:
                var url = ctx.address_urls[i].copy()
                capture(ctx, slug_name, url, file)
        return text_reply(
            200,
            "Cache-Control: no-store\nContent-Type: image/svg+xml",
            portal_placeholder(),
        )

    return text_reply(404, "Transfer-Encoding: chunked", String(""))


def send(req: PythonObject, reply: Reply) raises:
    if reply.file.byte_length() > 0:
        _ = req.reply_file(
            PythonObject(reply.status),
            PythonObject(reply.headers),
            PythonObject(reply.file),
        )
    else:
        _ = req.reply_text(
            PythonObject(reply.status),
            PythonObject(reply.headers),
            PythonObject(reply.body),
        )


def serve(port: Int, journal_file: String) raises:
    """Make the previews folder, listen on loopback, answer until stopped."""
    var previews = join_path(
        directory_of(absolute_path(journal_file)), "previews"
    )
    makedirs(previews, exist_ok=True)
    var listener = listen(port)
    print("Portal is at http://127.0.0.1:" + String(port) + "/", flush=True)
    var chromium = env_or("CHROMIUM", DEFAULT_CHROMIUM)
    var ctx = Context(journal_file, previews, port, chromium)
    while True:
        var req = next_request(listener)
        var host = String(req.host)
        var has_host = String(req.has_host) == "True"
        var target = String(req.target)
        var loopback = String(req.loopback)
        try:
            var reply = answer(ctx, host, has_host, target, loopback)
            send(req, reply)
        except e:
            # An answer that fails closes the request with an empty chunked 200.
            _ = e
            send(
                req,
                Reply(
                    200, "Transfer-Encoding: chunked", String(""), String("")
                ),
            )


def portal_command(args: List[String], journal_file: String) raises:
    """`vinci serve [port]`. The port is digits only, as NumberStyles.None reads it.
    """
    var port = DEFAULT_PORT
    if len(args) > 0:
        var text = args[0]
        var value = 0
        var valid = text.byte_length() > 0 and text.byte_length() <= 10
        for b in text.as_bytes():
            if b < 48 or b > 57:
                valid = False
            else:
                value = value * 10 + Int(b) - 48
        if not valid or value > 2147483647:
            raise user_error("<port> must be a number, got '" + text + "'")
        port = value
    serve(port, journal_file)
