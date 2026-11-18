"""Fixture checks for read_guard.py, run by tests/test_read_guard.mojo (make test).

No network is used. A name is resolved by a stub, and a connection is made by a stub that serves a
canned HTTP response over a socket pair, so the redirect and rebinding cases are exact. run()
returns the failed checks as text; an empty list means every check passed.
"""

from __future__ import annotations

import os
import shutil
import socket
import tempfile
import threading
import urllib.error
import urllib.request
from collections.abc import Callable
from pathlib import Path
from typing import Final

from . import read_guard
from .read_guard import Refused

PUBLIC_V4: Final = "93.184.216.34"
PUBLIC_V6: Final = "2606:2800:220:1:248:1893:25c8:1946"
RESPONSE_OK: Final = b"HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello"


def _redirect(location: str) -> bytes:
    return (
        f"HTTP/1.1 302 Found\r\nLocation: {location}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
    ).encode()


class Stub:
    """Records what the guard tried to reach, and serves the canned responses in order."""

    def __init__(self, names: dict[str, list[str]], responses: list[bytes]) -> None:
        self.names = names
        self.responses = list(responses)
        self.dialled: list[tuple[str, int]] = []
        self.requests: list[bytes] = []

    def resolve(self, host: str) -> list[str]:
        if host not in self.names:
            raise Refused(f"the host name {host} could not be resolved: stub")
        return list(self.names[host])

    def connect(self, address: tuple[str, int], timeout: float | None) -> socket.socket:
        self.dialled.append((address[0], address[1]))
        client, server = socket.socketpair()
        body = self.responses.pop(0) if self.responses else RESPONSE_OK

        def serve() -> None:
            received = b""
            while b"\r\n\r\n" not in received:
                chunk = server.recv(4096)
                if chunk == b"":
                    break
                received += chunk
            self.requests.append(received)
            server.sendall(body)
            server.close()

        threading.Thread(target=serve, daemon=True).start()
        return client


def _fetch(url: str, stub: Stub) -> str:
    """The text of a fetch through the guard, or the refusal's words."""
    opener = read_guard.guarded_opener(stub.resolve, stub.connect)
    try:
        with opener.open(urllib.request.Request(url), timeout=5) as response:
            return response.read().decode("utf-8")
    except Refused as err:
        return "refused: " + str(err)
    except (urllib.error.URLError, OSError) as err:
        return "failed: " + read_guard.describe_fetch_error(err)


def check_addresses(fails: list[str]) -> None:
    blocked = [
        "127.0.0.1",
        "127.8.9.10",
        "::1",
        "0.0.0.0",
        "::",
        "10.1.2.3",
        "172.16.0.1",
        "192.168.1.1",
        "169.254.169.254",
        "fe80::1",
        "fc00::1",
        "fd12:3456::1",
        "::ffff:127.0.0.1",
        "::ffff:10.0.0.1",
        "::ffff:169.254.1.1",
        "0:0:0:0:0:ffff:7f00:1",
        "224.0.0.1",
        "100.64.0.1",
    ]
    for text in blocked:
        if read_guard.address_problem(text) is None:
            fails.append(f"address {text} should be refused")
    for text in [PUBLIC_V4, PUBLIC_V6, "::ffff:93.184.216.34", "1.1.1.1"]:
        if read_guard.address_problem(text) is not None:
            fails.append(f"address {text} should be allowed")


def check_hosts(fails: list[str]) -> None:
    if read_guard.host_problem("rebind.test", ["127.0.0.1"]) is None:
        fails.append("a name that resolves to 127.0.0.1 should be refused")
    if read_guard.host_problem("mixed.test", [PUBLIC_V4, "10.0.0.5"]) is None:
        fails.append("a name with one private answer should be refused")
    if read_guard.host_problem("public.test", [PUBLIC_V4]) is not None:
        fails.append("a public name should be allowed")
    if read_guard.host_problem("empty.test", []) is None:
        fails.append("a name with no address should be refused")


def check_schemes(fails: list[str]) -> None:
    for url in ["file:///etc/hosts", "ftp://example.org/x", "javascript:alert(1)", "http:///x"]:
        if read_guard.scheme_problem(url) is None:
            fails.append(f"scheme check should refuse {url}")
    for url in ["https://example.org/a", "HTTP://example.org/"]:
        if read_guard.scheme_problem(url) is not None:
            fails.append(f"scheme check should allow {url}")


def check_paths(fails: list[str]) -> None:
    root = Path(tempfile.mkdtemp(prefix="vinci-guard-"))
    try:
        home = root / "home"
        outside = root / "outside"
        for folder in [
            home / "notes",
            home / ".ssh",
            home / ".config",
            home / ".vinci" / "backups" / "journal",
            outside,
        ]:
            folder.mkdir(parents=True, exist_ok=True)
        (home / "notes" / "a.md").write_text("alpha", encoding="utf-8")
        (home / ".ssh" / "key.md").write_text("secret", encoding="utf-8")
        (home / ".config" / "x.txt").write_text("secret", encoding="utf-8")
        (home / ".vinci" / "backups" / "journal" / "j.md").write_text("copy", encoding="utf-8")
        (outside / "secret.md").write_text("outside", encoding="utf-8")
        os.symlink(home / ".ssh" / "key.md", home / "link_to_key.md")
        os.symlink(outside / "secret.md", home / "escape.md")
        os.symlink(home / "notes", home / "notes_link")
        deny = read_guard.deny_list(str(home / ".vinci"), str(home))

        def refused(path: str) -> bool:
            try:
                read_guard.check_local_path(path, str(home), deny)
            except Refused:
                return True
            return False

        if refused(str(home / "notes" / "a.md")):
            fails.append("a note under home should be allowed")
        if refused(str(home / "notes_link" / "a.md")):
            fails.append("a folder link that stays under home should be allowed")
        for path, why in [
            (str(outside / "secret.md"), "a file outside home"),
            (str(home / ".." / "outside" / "secret.md"), "a .. escape"),
            (str(home / "escape.md"), "a symbolic link out of home"),
            (str(home / "link_to_key.md"), "a symbolic link into .ssh"),
            (str(home / ".ssh" / "key.md"), "a file in .ssh"),
            (str(home / ".config" / "x.txt"), "a file in .config"),
            (str(home / ".vinci" / "backups" / "journal" / "j.md"), "a vinci backup"),
        ]:
            if not refused(path):
                fails.append(f"{why} should be refused: {path}")
        # A folder whose name starts with a denied name is not denied: .sshx is not .ssh.
        (home / ".sshx").mkdir()
        (home / ".sshx" / "ok.md").write_text("fine", encoding="utf-8")
        if refused(str(home / ".sshx" / "ok.md")):
            fails.append("a folder named .sshx is not .ssh and should be allowed")
    finally:
        shutil.rmtree(root, ignore_errors=True)


def check_fetches(fails: list[str]) -> None:
    # A name that resolves to loopback is refused before any connection.
    stub = Stub({"rebind.test": ["127.0.0.1"]}, [])
    got = _fetch("http://rebind.test/", stub)
    if not got.startswith("refused:") or stub.dialled:
        fails.append(f"rebinding name: expected a refusal with no connection, got {got!r}")

    # A public page is read, dialled at the checked address, with the name in the Host header.
    stub = Stub({"public.test": [PUBLIC_V4]}, [RESPONSE_OK])
    got = _fetch("http://public.test/page", stub)
    if got != "hello":
        fails.append(f"public page: expected hello, got {got!r}")
    if stub.dialled != [(PUBLIC_V4, 80)]:
        fails.append(f"public page: expected the checked address to be dialled, got {stub.dialled}")
    if not stub.requests or b"Host: public.test" not in stub.requests[0]:
        fails.append("public page: the request should carry the host name")

    # A redirect to the metadata address is refused, and that hop is never dialled.
    stub = Stub(
        {"public.test": [PUBLIC_V4]}, [_redirect("http://169.254.169.254/latest/meta-data/")]
    )
    got = _fetch("http://public.test/", stub)
    if got.startswith("hello") or stub.dialled != [(PUBLIC_V4, 80)]:
        fails.append(
            f"redirect to 169.254.x.x: expected no second connection, got {got!r} {stub.dialled}"
        )

    # A redirect to a name that resolves to loopback is refused.
    stub = Stub(
        {"public.test": [PUBLIC_V4], "localhost": ["127.0.0.1"]},
        [_redirect("http://localhost:8080/")],
    )
    got = _fetch("http://public.test/", stub)
    if got.startswith("hello") or stub.dialled != [(PUBLIC_V4, 80)]:
        fails.append(f"redirect to localhost: expected refusal, got {got!r} {stub.dialled}")

    # A redirect to a loopback IP literal, in mapped IPv6 form, is refused.
    stub = Stub({"public.test": [PUBLIC_V4]}, [_redirect("http://[::ffff:127.0.0.1]/")])
    got = _fetch("http://public.test/", stub)
    if got.startswith("hello") or stub.dialled != [(PUBLIC_V4, 80)]:
        fails.append(f"redirect to mapped loopback: expected refusal, got {got!r} {stub.dialled}")

    # A redirect to a file address has no handler, so nothing is read.
    stub = Stub({"public.test": [PUBLIC_V4]}, [_redirect("file:///etc/hosts")])
    got = _fetch("http://public.test/", stub)
    if got.startswith("hello") or len(stub.dialled) != 1:
        fails.append(f"redirect to file: expected no read, got {got!r}")

    # A name with one private answer is refused whichever answer comes first.
    stub = Stub({"mixed.test": ["10.0.0.5", PUBLIC_V4]}, [])
    got = _fetch("http://mixed.test/", stub)
    if not got.startswith("refused:") or stub.dialled:
        fails.append(f"mixed answers: expected a refusal, got {got!r}")


def run() -> list[str]:
    """Every check. The list is the failures, empty when all pass."""
    fails: list[str] = []
    checks: list[Callable[[list[str]], None]] = [
        check_addresses,
        check_hosts,
        check_schemes,
        check_paths,
        check_fetches,
    ]
    for check in checks:
        try:
            check(fails)
        except Exception as err:  # noqa: BLE001 - a broken fixture is reported as a failure, not hidden
            fails.append(f"{check.__name__} raised {type(err).__name__}: {err}")
    return fails
