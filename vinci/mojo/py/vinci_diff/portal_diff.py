"""Differential test for `vinci serve`: the C# oracle against the Mojo portal, request by request.

For each journal kind the oracle and the candidate run in turn on the same free loopback
port, each with its own VINCI_HOME (a copy of the journal and a previews folder), and the
same requests are sent in the same order over raw sockets. The two runs are sequential,
not concurrent: the portal lists every other web page on this machine, so two live
portals would list each other, and the port numbers would differ between the pages.

Compared for every request: status, headers (all but Date, Server and Connection), and the
body byte for byte. Compared after each run: the journal file and the previews folder.

Ground truth that the Mojo side is held to lives in portal_truth/ (.NET 10 results for the
WebUtility, Uri and JSON details). Run from mojo/py:

    uv run python -m vinci_diff.portal_diff --candidate /abs/path/mojo/build/vinci
"""

from __future__ import annotations

import argparse
import hashlib
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
from collections.abc import Callable, Sequence
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import cast

from .oracle import ORACLE

MOJO_DIR = Path(__file__).resolve().parents[2]
DEFAULT_ORACLE = ORACLE
DEFAULT_CANDIDATE = MOJO_DIR / "build" / "vinci"
DEFAULT_JOURNAL = Path.home() / ".vinci" / "staging" / "journal.jsonl"
MOJO_PY = MOJO_DIR / "py"
NOW = "2026-11-18T10:00:00+00:00"
TIMEOUT_SECONDS = 20.0
READY_SECONDS = 20.0
IGNORED_HEADERS = frozenset({"date", "server", "connection"})
PNG_1X1 = bytes.fromhex(
    "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4"
    "890000000d49444154789c63f8cfc0f01f0005000201a5f6453d0000000049454e44ae426082"
)


# --- fixtures: local pages the portal discovers --------------------------------------------


class _FixtureHandler(BaseHTTPRequestHandler):
    """One page per fixture server, chosen by its name. Each kind answers the same way."""

    protocol_version = "HTTP/1.0"

    def do_GET(self) -> None:
        kind = cast(_FixtureServer, self.server).kind
        if kind == "titled":
            self._send(
                200,
                "text/html; charset=utf-8",
                b"<!doctype html><html><head><title>  Fixture &amp; &#x41;&copy;&nbsp;&#8217;"
                b"&unknown;\n  Title </title></head><body>A</body></html>",
            )
        elif kind == "plain":
            self._send(200, "text/plain", b"<title>not html</title>")
        elif kind == "missing":
            self._send(404, "text/html", b"<title>Gone</title>")
        elif kind == "redirect":
            self.send_response(302)
            self.send_header("Location", "/home")
            self.send_header("Content-Length", "0")
            self.end_headers()
        elif kind == "home":
            self._send(200, "text/html", b"<html><title>Redirected</title></html>")
        elif kind == "slow":
            time.sleep(2.5)
            self._send(200, "text/html", b"<title>Too slow</title>")
        elif kind == "untitled":
            self._send(200, "text/html", b"<html><body>no title here</body></html>")
        elif kind == "latin1":
            self._send(
                200,
                "text/html; charset=iso-8859-1",
                b"<title>Caf\xe9 &lt;ok&gt;</title>",
            )
        elif kind == "upper":
            self._send(200, "text/html", b'<TITLE lang="en">  Upper\tCase  </TITLE>')
        elif kind == "tagged":
            self._send(200, "text/html", b"<title>one<b>two</b></title>")
        elif kind == "mixedcase":
            self._send(200, "Text/HTML", b"<title>Mixed media type</title>")
        else:
            self._send(500, "text/plain", b"no")

    def _send(self, status: int, content_type: str, body: bytes) -> None:
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format: str, *args: object) -> None:
        """Quiet."""


class _FixtureServer(ThreadingHTTPServer):
    def __init__(self, kind: str) -> None:
        self.kind = kind
        super().__init__(("127.0.0.1", 0), _FixtureHandler)


FIXTURE_KINDS = (
    "titled",
    "plain",
    "missing",
    "redirect",
    "slow",
    "untitled",
    "latin1",
    "upper",
    "tagged",
    "mixedcase",
)


def start_fixtures() -> dict[str, int]:
    """One listening server per kind, kept up for the whole run. Returns kind -> port."""
    ports: dict[str, int] = {}
    for kind in FIXTURE_KINDS:
        server = _FixtureServer(kind)
        ports[kind] = int(server.server_address[1])
        threading.Thread(target=server.serve_forever, daemon=True).start()
    return ports


# --- raw HTTP ---------------------------------------------------------------------------------


@dataclass(frozen=True)
class Step:
    name: str
    raw: bytes
    head: bool = False
    v6: bool = False


@dataclass(frozen=True)
class Reply:
    status: int | None
    headers: tuple[tuple[str, str], ...]
    body: bytes
    error: str | None


def raw_request(
    method: str,
    target: bytes,
    host: str | None,
    body: bytes = b"",
    version: bytes = b"HTTP/1.1",
    extra: Sequence[str] = (),
) -> bytes:
    lines = [method.encode("latin-1") + b" " + target + b" " + version]
    if host is not None:
        lines.append(f"Host: {host}".encode("latin-1"))
    lines.append(b"Connection: close")
    if body:
        lines.append(f"Content-Length: {len(body)}".encode("latin-1"))
    lines.extend(line.encode("latin-1") for line in extra)
    return b"\r\n".join(lines) + b"\r\n\r\n" + body


def _decode_chunked(data: bytes) -> bytes:
    out = b""
    rest = data
    while True:
        line, _, rest = rest.partition(b"\r\n")
        size = int(line.split(b";")[0] or b"0", 16)
        if size == 0:
            return out
        out += rest[:size]
        rest = rest[size + 2 :]


def send_raw(port: int, raw: bytes, head: bool, v6: bool) -> Reply:
    family = socket.AF_INET6 if v6 else socket.AF_INET
    address = ("::1", port, 0, 0) if v6 else ("127.0.0.1", port)
    try:
        with socket.socket(family, socket.SOCK_STREAM) as sock:
            sock.settimeout(TIMEOUT_SECONDS)
            sock.connect(address)
            sock.sendall(raw)
            data = b""
            while True:
                chunk = sock.recv(65536)
                if not chunk:
                    break
                data += chunk
    except OSError as exc:
        return Reply(None, (), b"", f"{type(exc).__name__}: {exc}")
    head_part, sep, body = data.partition(b"\r\n\r\n")
    if not sep:
        return Reply(None, (), data, "no header terminator")
    lines = head_part.decode("latin-1").split("\r\n")
    parts = lines[0].split(" ", 2)
    try:
        status = int(parts[1])
    except (IndexError, ValueError):
        return Reply(None, (), data, f"bad status line {lines[0]!r}")
    headers: list[tuple[str, str]] = []
    for line in lines[1:]:
        name, _, value = line.partition(":")
        if name.strip().lower() in IGNORED_HEADERS:
            continue
        headers.append((name.strip().lower(), value.strip()))
    headers.sort()
    if head:
        payload = b""
    elif ("transfer-encoding", "chunked") in headers:
        payload = _decode_chunked(body)
    else:
        length = dict(headers).get("content-length")
        payload = body[: int(length)] if length is not None else body
    return Reply(status, tuple(headers), payload, None)


# --- the requests ------------------------------------------------------------------------------


def build_steps(port: int, fixtures: dict[str, int]) -> list[Step]:
    """Every route, GET and POST and the rest, bad input and Host variants, in a fixed order."""
    own = f"127.0.0.1:{port}"
    steps: list[Step] = []

    def get(name: str, target: bytes, host: str | None = own) -> None:
        steps.append(Step(name, raw_request("GET", target, host)))

    get("page", b"/")
    get("page again (scan cached)", b"/")
    get("page with query", b"/?x=1&y=%20")
    get("page with fragment", b"/#frag")
    get("index.html", b"/index.html")
    get("unknown route", b"/nope")
    get("double slash", b"//")
    get("dot segments to root", b"/%2e%2e/")
    get("dot segments", b"/a/../")
    get("dot and empty segment", b"/./")
    get("backslash path", b"/a\\b")
    get("trailing slash after png", b"/previews/x.png/")
    get("previews folder", b"/previews/")
    get("previews bare dot png", b"/previews/.png")
    get("preview seeded", b"/previews/qwik-docs.png")
    get("preview seeded, escaped title", b"/previews/Qwik%20Docs.png")
    get("preview seeded, lower escaped", b"/previews/qwik%20docs.png")
    get("preview discovered and seeded", f"/previews/local-{fixtures['titled']}.png".encode())
    get("preview discovered, not seeded", f"/previews/local-{fixtures['plain']}.png".encode())
    get("preview unknown slug", b"/previews/unknown-thing.png")
    get("preview escaped letter", b"/previews/%41.png")
    get("preview escaped slash", b"/previews/a%2Fb.png")
    get("preview escaped unicode", b"/previews/%C3%A9.png")
    get("preview raw utf-8", "/previews/é.png".encode())
    get("preview bad escape", b"/previews/%.png")
    get("preview dot escape", b"/previews/%2e.png")
    get("preview tilde escape", b"/previews/x%7Ey.png")
    get("preview lowercase escape kept", b"/previews/a%2fb.png")
    get("preview html chars", b"/previews/a<b>.png")
    get("preview not png", b"/previews/bad")
    get("preview with query", b"/previews/qwik-docs.png?v=2")
    get("long query", b"/?" + b"a" * 3000)
    get("star target", b"*")
    steps.append(Step("POST page", raw_request("POST", b"/", own, b"title=x&url=http://x")))
    steps.append(Step("POST bookmark route", raw_request("POST", b"/bookmark", own, b"t=1")))
    steps.append(Step("POST preview", raw_request("POST", b"/previews/qwik-docs.png", own, b"x")))
    steps.append(Step("PUT page", raw_request("PUT", b"/", own, b"x")))
    steps.append(Step("DELETE page", raw_request("DELETE", b"/", own)))
    steps.append(Step("OPTIONS page", raw_request("OPTIONS", b"/", own)))
    steps.append(Step("PATCH page", raw_request("PATCH", b"/", own, b"{}")))
    steps.append(Step("FOO page", raw_request("FOO", b"/", own)))
    steps.append(Step("lower-case get", raw_request("get", b"/", own)))
    steps.append(Step("HEAD page", raw_request("HEAD", b"/", own), head=True))
    steps.append(
        Step(
            "HEAD preview",
            raw_request("HEAD", b"/previews/qwik-docs.png", own),
            head=True,
        )
    )
    steps.append(Step("HEAD unknown", raw_request("HEAD", b"/nope", own), head=True))
    steps.append(Step("GET with body", raw_request("GET", b"/", own, b"abc")))
    steps.append(Step("HTTP/1.0 page", raw_request("GET", b"/", own, version=b"HTTP/1.0")))
    steps.append(Step("extra header", raw_request("GET", b"/", own, extra=["X-Test: 1"])))
    for name, host in (
        ("host localhost", f"localhost:{port}"),
        ("host LOCALHOST upper", f"LOCALHOST:{port}"),
        ("host 127.0.0.1 no port", "127.0.0.1"),
        ("host localhost no port", "localhost"),
        ("host other name", f"evil.example:{port}"),
        ("host other port", f"127.0.0.1:{port + 1}"),
        ("host ipv6 literal", f"[::1]:{port}"),
        ("host empty", ""),
        ("host bare ipv6", "::1"),
        ("host bare ipv6 with port", f"::1:{port}"),
    ):
        steps.append(Step(f"page, {name}", raw_request("GET", b"/", host)))
    steps.append(Step("page, no host header", raw_request("GET", b"/", None)))
    steps.append(
        Step("page over ipv6 loopback", raw_request("GET", b"/", f"localhost:{port}"), v6=True)
    )
    steps.append(
        Step(
            "preview over ipv6 loopback",
            raw_request("GET", b"/previews/qwik-docs.png", f"[::1]:{port}"),
            v6=True,
        )
    )
    steps.append(Step("bogus request line", b"GET\r\n\r\n"))
    steps.append(
        Step("page, no host, bad version", raw_request("GET", b"/", own, version=b"HTTP/9.9"))
    )
    return steps


def short_steps(port: int) -> list[Step]:
    own = f"127.0.0.1:{port}"
    return [
        Step("page", raw_request("GET", b"/", own)),
        Step("page again", raw_request("GET", b"/", own)),
        Step("unknown route", raw_request("GET", b"/nope", own)),
        Step("preview", raw_request("GET", b"/previews/qwik-docs.png", own)),
        Step("host other", raw_request("GET", b"/", f"evil.example:{port}")),
    ]


# --- journals and repositories ----------------------------------------------------------------


def _git(repo: Path, *args: str) -> None:
    env = {
        **os.environ,
        "GIT_AUTHOR_NAME": "fixture",
        "GIT_AUTHOR_EMAIL": "fixture@example.invalid",
        "GIT_COMMITTER_NAME": "fixture",
        "GIT_COMMITTER_EMAIL": "fixture@example.invalid",
    }
    subprocess.run(["git", "-C", str(repo), *args], check=True, capture_output=True, env=env)


def make_repo(root: Path, files: dict[str, bytes], untracked: dict[str, bytes]) -> Path:
    """A git repository with the files committed, and the untracked ones left on disk."""
    if root.exists():
        shutil.rmtree(root)
    root.mkdir(parents=True)
    _git(root, "init", "-q")
    for name, data in files.items():
        target = root / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
    _git(root, "add", "-A")
    _git(root, "commit", "-q", "-m", "fixture")
    for name, data in untracked.items():
        target = root / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
    return root


def portal_json_a(port: int, fixtures: dict[str, int]) -> bytes:
    text = f"""[
 {{"title":"Alpha Docs","url":"https://alpha.example/docs","description":"Alpha documentation","tags":["docs","alpha"]}},
 {{"title":"  Padded Title  ","url":"http://padded.example/"}},
 {{"title":"","url":"https://empty-title.example/"}},
 {{"title":"No scheme","url":"ftp://ftp.example/"}},
 {{"title":"Dup URL","url":"https://alpha.example/docs"}},
 {{"title":"Tagged \\u00c9 \\u2265 <i>","url":"https://unicode.example/\\u00e9","description":"caf\\u00e9 \\"quoted\\" <b>x</b> & more","tags":["Mixed","mixed","\\u00dcber",3,null,"b&c"]}},
 {{"title":"Local Bookmark","url":"http://LOCALHOST:{fixtures["untitled"]}/x"}},
 {{"title":"Own port","url":"http://127.0.0.1:{port}/"}},
 {{"title":"Fixture titled","url":"http://localhost:{fixtures["titled"]}/"}},
 {{"title":"www host","url":"https://www.Example.org:8443/a/b?c=d#e"}},
 {{"title":"Dup key","title":"Second","url":"https://dupkey.example/"}},
 {{"title":"Empty description","url":"https://emptydesc.example/","description":""}},
 {{"title":"Non-string url","url":5}},
 {{"title":"Tags not an array","url":"https://tags.example/","tags":"nope"}},
 "not an object",
 {{"title":"Trailing","url":"https://trail.example/"}}
]
"""
    return text.encode("utf-8")


def repos_and_journals(
    base: Path, port: int, fixtures: dict[str, int], journal_src: Path
) -> dict[str, Path]:
    """Creates the repositories (fixed paths, so both runs see the same ones) and the journal
    kinds. Returns kind -> journal file."""
    repos = base / "repos"
    a = make_repo(
        repos / "a",
        {
            "portal.json": portal_json_a(port, fixtures),
            "cold-storage/portal.json": b'[{"title":"Cold","url":"https://cold.example/"}]',
            "deep/er/portal.json": b'[{"title":"Deep","url":"https://deep.example/"}]',
            "sub/portal.json": b'[{"title":"Sub","url":"https://sub.example/"}]',
        },
        {"untracked/portal.json": b'[{"title":"Untracked","url":"https://untracked.example/"}]'},
    )
    b = make_repo(
        repos / "b",
        {
            "portal.json": b'[{"title":"Beta","url":"https://alpha.example/docs"},'
            b'{"title":"Beta two","url":"https://beta.example/"}]',
            "broken/portal.json": b"[1,]",
        },
        {},
    )
    c = make_repo(
        repos / "retired",
        {"portal.json": b'[{"title":"Retired","url":"https://retired.example/"}]'},
        {},
    )
    d = make_repo(
        repos / "lone",
        {"portal.json": b'[{"title":"\\ud800 lone","url":"https://lone.example/"}]'},
        {},
    )
    deep = a / "deep" / "er"
    portal_lines = [
        '{"at":"2026-11-01T09:00:00-06:00","kind":"idea","id":"i1","text":"alpha"}',
        f'{{"at":"2026-11-01T09:00:00-06:00","kind":"home","id":"i1","text":"{a}"}}',
        '{"at":"2026-11-01T09:01:00-06:00","kind":"idea","id":"i2","text":"beta"}',
        f'{{"at":"2026-11-01T09:01:00-06:00","kind":"home","id":"i2","text":"{b}"}}',
        '{"at":"2026-11-01T09:02:00-06:00","kind":"idea","id":"i3","text":"retired"}',
        f'{{"at":"2026-11-01T09:02:00-06:00","kind":"home","id":"i3","text":"{c}"}}',
        '{"at":"2026-11-01T09:03:00-06:00","kind":"stage","id":"i3","text":"Retired"}',
        '{"at":"2026-11-01T09:04:00-06:00","kind":"idea","id":"i4","text":"deep"}',
        f'{{"at":"2026-11-01T09:04:00-06:00","kind":"home","id":"i4","text":"{deep}"}}',
        (
            '{"at":"2026-11-02T09:00:00-06:00","kind":"bookmark","id":"b1","text":"Qwik Docs",'
            '"url":"https://qwik.dev/","why":"Official documentation","tags":["docs","qwik"]}'
        ),
        (
            '{"at":"2026-11-02T09:01:00-06:00","kind":"bookmark","id":"b2","text":"Gone",'
            '"url":"https://gone.example/"}'
        ),
        '{"at":"2026-11-02T09:02:00-06:00","kind":"unbookmark","id":"b2"}',
        (
            '{"at":"2026-11-02T09:03:00-06:00","kind":"bookmark","id":"b3","text":"Angle <brackets> & \\"quotes\\" \\u00e9",'
            '"url":"https://angle.example/path?q=1&r=2","why":"Ünïcode \\u2026 mark","tags":["Zeta","alpha","ALPHA"]}'
        ),
        (
            '{"at":"2026-11-02T09:04:00-06:00","kind":"bookmark","id":"b4","text":"Upper host",'
            '"url":"HTTP://WWW.UPPER.example:8080/","tags":[]}'
        ),
        (
            '{"at":"2026-11-02T09:05:00-06:00","kind":"bookmark","id":"b5","text":"Fixture via bookmark",'
            f'"url":"http://localhost:{fixtures["titled"]}/","why":"covers the fixture"}}'
        ),
        (
            '{"at":"2026-11-02T09:06:00-06:00","kind":"bookmark","id":"b6","text":"Mailto",'
            '"url":"mailto:someone@example.com"}'
        ),
        (
            '{"at":"2026-11-02T09:07:00-06:00","kind":"bookmark","id":"b7","text":"Tab title",'
            '"url":"https://tabs.example/","why":"  two  spaces  "}'
        ),
        (
            '{"at":"2026-11-02T09:08:00-06:00","kind":"bookmark","id":"b8","text":"Relative path",'
            '"url":"/relative/path"}'
        ),
        (
            '{"at":"2026-11-02T09:09:00-06:00","kind":"bookmark","id":"b9","text":"Scheme-relative",'
            '"url":"//relative.example:9090/x"}'
        ),
    ]
    journals: dict[str, Path] = {}
    staging = base / "journals" / "staging.jsonl"
    staging.parent.mkdir(parents=True, exist_ok=True)
    if journal_src.exists():
        shutil.copyfile(journal_src, staging)
    else:
        staging.write_text("", encoding="utf-8")
    journals["staging"] = staging
    portal = base / "journals" / "portal.jsonl"
    portal.write_text("\n".join(portal_lines) + "\n", encoding="utf-8")
    journals["portal"] = portal
    broken = base / "journals" / "broken.jsonl"
    broken.write_text("\n".join(portal_lines + ["{not json"]) + "\n", encoding="utf-8")
    journals["broken"] = broken
    crash = base / "journals" / "crash.jsonl"
    crash_lines = portal_lines + [
        '{"at":"2026-11-03T09:00:00-06:00","kind":"idea","id":"i5","text":"lone"}',
        f'{{"at":"2026-11-03T09:00:00-06:00","kind":"home","id":"i5","text":"{d}"}}',
    ]
    crash.write_text("\n".join(crash_lines) + "\n", encoding="utf-8")
    journals["crash"] = crash
    return journals


# --- running one side ---------------------------------------------------------------------------


@dataclass(frozen=True)
class SideResult:
    replies: tuple[Reply, ...]
    journal_sha: str
    previews: tuple[tuple[str, str], ...]
    stderr_tail: str
    startup_error: str | None


def _digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _previews_state(folder: Path) -> tuple[tuple[str, str], ...]:
    if not folder.is_dir():
        return ()
    return tuple(sorted((p.name, _digest(p.read_bytes())) for p in folder.iterdir() if p.is_file()))


def _listening(port: int) -> bool:
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=0.5):
            return True
    except OSError:
        return False


def run_side(
    label: str,
    argv: list[str],
    run_dir: Path,
    journal: Path,
    port: int,
    steps_for: Callable[[int], list[Step]],
    fixtures: dict[str, int],
) -> SideResult:
    if run_dir.exists():
        shutil.rmtree(run_dir)
    home = run_dir / "home"
    home.mkdir(parents=True)
    shutil.copyfile(journal, home / "journal.jsonl")
    previews = home / "previews"
    previews.mkdir()
    (previews / "qwik-docs.png").write_bytes(PNG_1X1)
    (previews / f"local-{fixtures['titled']}.png").write_bytes(PNG_1X1 + b"x")
    env_file = run_dir / "empty.env"
    env_file.write_text("", encoding="utf-8")
    env = {
        "PATH": os.environ.get("PATH", ""),
        "HOME": os.environ.get("HOME", str(run_dir)),
        "LANG": "C.UTF-8",
        "VINCI_HOME": str(home),
        "VINCI_ENV_FILE": str(env_file),
        "VINCI_NO_TRACK": "1",
        "VINCI_NOW": NOW,
        "CHROMIUM": "/nonexistent-chromium",
        "VINCI_MOJO_PY": str(MOJO_PY),
    }
    process = subprocess.Popen(
        [*argv, str(port)],
        cwd=run_dir,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    deadline = time.monotonic() + READY_SECONDS
    while not _listening(port):
        if process.poll() is not None or time.monotonic() > deadline:
            process.kill()
            _, err = process.communicate()
            tail = err.decode("utf-8", "replace").strip().splitlines()[-3:]
            return SideResult((), "", (), "\n".join(tail), f"{label} did not listen")
        time.sleep(0.05)
    replies: list[Reply] = []
    try:
        for step in steps_for(port):
            replies.append(send_raw(port, step.raw, step.head, step.v6))
    finally:
        process.terminate()
        try:
            _, err = process.communicate(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            _, err = process.communicate()
    return SideResult(
        tuple(replies),
        _digest((home / "journal.jsonl").read_bytes()),
        _previews_state(previews),
        err.decode("utf-8", "replace").strip()[-400:],
        None,
    )


# --- comparing ---------------------------------------------------------------------------------


@dataclass(frozen=True)
class Divergence:
    index: int
    name: str
    detail: list[str]


def compare_reply(index: int, name: str, want: Reply, got: Reply) -> list[str]:
    diffs: list[str] = []
    if (want.error or got.error) and (want.error != got.error or want.status != got.status):
        diffs.append(f"error: oracle={want.error!r} candidate={got.error!r}")
    if want.status != got.status:
        diffs.append(f"status: oracle={want.status} candidate={got.status}")
    if want.headers != got.headers:
        want_map = dict(want.headers)
        got_map = dict(got.headers)
        for key in sorted(set(want_map) | set(got_map)):
            if want_map.get(key) != got_map.get(key):
                diffs.append(
                    f"header {key}: oracle={want_map.get(key)!r} candidate={got_map.get(key)!r}"
                )
    if want.body != got.body:
        at = next(
            (i for i, (x, y) in enumerate(zip(want.body, got.body, strict=False)) if x != y),
            min(len(want.body), len(got.body)),
        )
        diffs.append(
            f"body: oracle {len(want.body)} bytes, candidate {len(got.body)} bytes, "
            f"first difference at byte {at}"
        )
        diffs.append(f"  oracle:    {want.body[max(0, at - 40) : at + 80]!r}")
        diffs.append(f"  candidate: {got.body[max(0, at - 40) : at + 80]!r}")
    return diffs


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="python -m vinci_diff.portal_diff",
        description="Compare `vinci serve` in the C# oracle and the Mojo portal, request by request.",
    )
    parser.add_argument("--oracle", type=Path, default=DEFAULT_ORACLE)
    parser.add_argument("--candidate", type=Path, default=DEFAULT_CANDIDATE)
    parser.add_argument(
        "--journal", type=Path, default=DEFAULT_JOURNAL, help="copied as the staging journal"
    )
    parser.add_argument(
        "--kinds", default="staging,portal,broken,crash", help="journal kinds to run"
    )
    parser.add_argument(
        "--work", type=Path, default=None, help="work directory (default: a temp dir)"
    )
    args = parser.parse_args(argv)
    for label, path in (("oracle", args.oracle), ("candidate", args.candidate)):
        if not path.is_file() or not os.access(path, os.X_OK):
            print(f"{label} is not an executable file: {path}", file=sys.stderr)
            return 2
    base = args.work or Path(tempfile.mkdtemp(prefix="portal-diff-"))
    base.mkdir(parents=True, exist_ok=True)
    fixtures = start_fixtures()
    port = _free_port()
    journals = repos_and_journals(base, port, fixtures, args.journal)
    kinds = [k for k in args.kinds.split(",") if k]
    divergences: list[tuple[str, Divergence]] = []
    rows: list[str] = []
    total_steps = 0
    total_matching = 0
    channel_checks = 0
    channel_matching = 0
    for kind in kinds:
        journal = journals[kind]
        original = _digest(journal.read_bytes())

        def steps_for(p: int, kind: str = kind) -> list[Step]:
            return build_steps(p, fixtures) if kind in ("staging", "portal") else short_steps(p)

        oracle = run_side(
            f"oracle/{kind}",
            [str(args.oracle), "serve"],
            base / "runs" / f"{kind}-oracle",
            journal,
            port,
            steps_for,
            fixtures,
        )
        candidate = run_side(
            f"candidate/{kind}",
            [str(args.candidate), "serve"],
            base / "runs" / f"{kind}-candidate",
            journal,
            port,
            steps_for,
            fixtures,
        )
        names = [s.name for s in steps_for(port)]
        matching = 0
        if oracle.startup_error or candidate.startup_error:
            rows.append(
                f"{kind:8} startup failed: oracle={oracle.startup_error or 'ok'} "
                f"candidate={candidate.startup_error or 'ok'}"
            )
            divergences.append(
                (kind, Divergence(-1, "startup", [oracle.stderr_tail, candidate.stderr_tail]))
            )
            total_steps += len(names)
            continue
        for i, name in enumerate(names):
            want = oracle.replies[i]
            got = candidate.replies[i]
            diffs = compare_reply(i, name, want, got)
            if diffs:
                divergences.append((kind, Divergence(i, name, diffs)))
            else:
                matching += 1
        total_steps += len(names)
        total_matching += matching
        channel_checks += 2
        same_journal = oracle.journal_sha == candidate.journal_sha == original
        same_previews = oracle.previews == candidate.previews
        channel_matching += int(same_journal) + int(same_previews)
        if not same_journal:
            divergences.append(
                (kind, Divergence(-1, "journal file after the run", ["journal changed or differs"]))
            )
        if not same_previews:
            divergences.append(
                (
                    kind,
                    Divergence(
                        -1,
                        "previews folder after the run",
                        [f"oracle={oracle.previews}", f"candidate={candidate.previews}"],
                    ),
                )
            )
        rows.append(
            f"{kind:8} steps {len(names):3}  matching {matching:3}  "
            f"journal {'same' if same_journal else 'DIFFERS'}  "
            f"previews {'same' if same_previews else 'DIFFERS'}"
        )
    print("portal differential test")
    print(f"  oracle:    {args.oracle}")
    print(f"  candidate: {args.candidate}")
    print(f"  work dir:  {base}")
    print()
    for row in rows:
        print("  " + row)
    for kind, div in divergences:
        print()
        print(f"DIVERGED  {kind}  step {div.index + 1 if div.index >= 0 else '-'}  {div.name}")
        for line in div.detail[:24]:
            print(f"    {line}")
    print()
    print("summary")
    print(f"  journal kinds:    {len(kinds)}  (each run against the oracle and the candidate)")
    print(f"  requests run:     {total_steps}")
    print(f"  requests matching:{total_matching:>4}")
    print(f"  state checks:     {channel_checks}  matching {channel_matching}")
    print(f"  diverged:         {len(divergences)}")
    return 0 if not divergences else 1


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


if __name__ == "__main__":
    sys.exit(main())
