"""What the chat's `read` tool may fetch: web addresses and files, checked before any byte is read.

Web: only http and https. The host name is resolved and every address it gives must be a public
one. Loopback (127.0.0.0/8, ::1), link-local (169.254.0.0/16, fe80::/10), private (RFC 1918 and
fc00::/7), unspecified (0.0.0.0, ::), multicast and every other address that is not global are
refused. An IPv4-mapped IPv6 address (::ffff:a.b.c.d) is judged by its IPv4 part. The connection is
made to the address that was checked (not to the name again), so a name that changes its answer
between the check and the connect cannot reach a private address. Every redirect hop goes through
the same check. A proxy from the environment is never used, because the check would not cover it.

Files: only a path under the person's home directory, and never under one of the secret folders
the deny list names (SSH and GPG keys, cloud credentials, the desktop keyring, the Claude Code
settings, the vinci backups). The path is resolved first (`..` and symbolic links are followed),
then checked, so a link from inside the home to outside it is refused.

The functions that decide are pure, or take the resolver and the connect function as arguments,
so mojo/tests/test_read_guard.mojo can check them with fixtures (read_guard_checks.py). Stdlib only.
"""

from __future__ import annotations

import http.client
import ipaddress
import os
import socket
import ssl
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Callable
from typing import Final

ALLOWED_SCHEMES: Final = ("http", "https")
SECRET_FOLDERS: Final = (
    ".ssh",
    ".gnupg",
    ".aws",
    ".config",
    os.path.join(".local", "share", "keyrings"),
    ".claude",
)
VINCI_BACKUPS: Final = os.path.join("backups")
CONNECT_TIMEOUT_S: Final = 30

Resolver = Callable[[str], list[str]]
Connector = Callable[[tuple[str, int], float | None], socket.socket]


class Refused(Exception):
    """The source may not be read. The message is what the tool tells the model."""


def system_resolver(host: str) -> list[str]:
    """Every address the system resolver gives for host, as text."""
    try:
        infos = socket.getaddrinfo(host, None, type=socket.SOCK_STREAM)
    except socket.gaierror as err:
        raise Refused(f"the host name {host} could not be resolved: {err.strerror}") from err
    found: list[str] = []
    for info in infos:
        address = str(info[4][0])
        if address not in found:
            found.append(address)
    return found


def system_connector(address: tuple[str, int], timeout: float | None) -> socket.socket:
    """A TCP connection to address (an IP and a port), as socket.create_connection makes it."""
    return socket.create_connection(address, timeout)


def address_problem(text: str) -> str | None:
    """Why an address may not be read, or None when it is a public address.

    text is an IPv4 or IPv6 address. Raises ValueError for anything that is not an address.
    """
    address = ipaddress.ip_address(text)
    if isinstance(address, ipaddress.IPv6Address) and address.ipv4_mapped is not None:
        address = address.ipv4_mapped
    if address.is_unspecified:
        return "an unspecified address"
    if address.is_loopback:
        return "a loopback address (this computer)"
    if address.is_link_local:
        return "a link-local address"
    if address.is_private:
        return "a private address (a home or office network, or a unique-local address)"
    if address.is_multicast:
        return "a multicast address"
    if not address.is_global:
        return "an address that is not public"
    return None


def host_problem(host: str, addresses: list[str]) -> str | None:
    """Why a host may not be read, given every address it resolved to. Any refused address
    refuses the host, so a name that answers with a public and a private address is refused."""
    if len(addresses) == 0:
        return f"the host name {host} has no address"
    for text in addresses:
        reason = address_problem(text)
        if reason is not None:
            if text == host:
                return f"the address {host} is {reason}"
            return f"the host name {host} resolves to {text}, which is {reason}"
    return None


def scheme_problem(url: str) -> str | None:
    """Why a web address may not be read by its scheme or its host, or None."""
    parts = urllib.parse.urlsplit(url)
    if parts.scheme.lower() not in ALLOWED_SCHEMES:
        return "only http:// and https:// addresses can be read"
    if not parts.hostname:
        return "the address has no host name"
    return None


def pinned_address(url: str, resolve: Resolver) -> str:
    """The address the connection will use for url, after every check. Raises Refused."""
    problem = scheme_problem(url)
    if problem is not None:
        raise Refused(problem)
    host = urllib.parse.urlsplit(url).hostname or ""
    try:
        ipaddress.ip_address(host)
        addresses = [host]
    except ValueError:
        addresses = resolve(host)
    problem = host_problem(host, addresses)
    if problem is not None:
        raise Refused(problem)
    return addresses[0]


def path_problem(path: str, home: str, deny: list[str]) -> str | None:
    """Why a file may not be read, given the person's home directory and the folders denied.

    path must already be resolved (realpath): this function does no resolving of its own, so
    a symbolic link has been followed before it gets here. Returns None for an allowed path.
    """
    if not (path == home or path.startswith(home.rstrip(os.sep) + os.sep)):
        return "the file is outside your home folder"
    for folder in deny:
        if path == folder or path.startswith(folder.rstrip(os.sep) + os.sep):
            return "the file is in a folder that holds secrets, so it cannot be read"
    return None


def check_local_path(path: str, home: str, deny: list[str]) -> str:
    """The resolved path of a file that may be read. Raises Refused for a file outside home or
    in a denied folder. A missing file is not refused here: the caller says there is none."""
    resolved = os.path.realpath(os.path.abspath(os.path.expanduser(path)))
    real_home = os.path.realpath(home)
    real_deny = [os.path.realpath(folder) for folder in deny]
    problem = path_problem(resolved, real_home, real_deny)
    if problem is not None:
        raise Refused(f"{problem}: {resolved}")
    return resolved


def deny_list(vinci_home: str, person_home: str) -> list[str]:
    """The folders no file read may come from: the secret folders under the person's home, and
    the vinci backups under vinci's own home."""
    folders = [os.path.join(person_home, name) for name in SECRET_FOLDERS]
    folders.append(os.path.join(vinci_home, VINCI_BACKUPS))
    return folders


class _PinnedHTTP(http.client.HTTPConnection):
    """An HTTP connection that dials the address already checked, and keeps the host name for
    the Host header."""

    pinned: str
    connector: Connector

    def __init__(self, host: str, pinned: str, connector: Connector, **kwargs: object) -> None:
        super().__init__(host, **kwargs)  # type: ignore[arg-type]
        self.pinned = pinned
        self.connector = connector

    def connect(self) -> None:
        self.sock = self.connector((self.pinned, self.port), self.timeout)


class _PinnedHTTPS(http.client.HTTPSConnection):
    """An HTTPS connection that dials the checked address and verifies the certificate for the
    host name."""

    pinned: str
    connector: Connector
    tls: ssl.SSLContext

    def __init__(
        self,
        host: str,
        pinned: str,
        connector: Connector,
        tls: ssl.SSLContext,
        **kwargs: object,
    ) -> None:
        super().__init__(host, context=tls, **kwargs)  # type: ignore[arg-type]
        self.pinned = pinned
        self.connector = connector
        self.tls = tls

    def connect(self) -> None:
        sock = self.connector((self.pinned, self.port), self.timeout)
        wrapped: socket.socket = self.tls.wrap_socket(sock, server_hostname=self.host)
        self.sock = wrapped


class _GuardedHTTP(urllib.request.HTTPHandler):
    def __init__(self, resolve: Resolver, connector: Connector) -> None:
        super().__init__()
        self._resolve = resolve
        self._connector = connector

    def http_open(self, req: urllib.request.Request) -> http.client.HTTPResponse:
        pinned = pinned_address(req.full_url, self._resolve)
        connector = self._connector

        def factory(host: str, **kwargs: object) -> http.client.HTTPConnection:
            return _PinnedHTTP(host, pinned, connector, **kwargs)

        return self.do_open(factory, req)  # type: ignore[arg-type,no-any-return]


class _GuardedHTTPS(urllib.request.HTTPSHandler):
    def __init__(self, resolve: Resolver, connector: Connector) -> None:
        super().__init__()
        self._resolve = resolve
        self._connector = connector

    def https_open(self, req: urllib.request.Request) -> http.client.HTTPResponse:
        pinned = pinned_address(req.full_url, self._resolve)
        connector = self._connector

        tls = ssl.create_default_context()

        def factory(host: str, **kwargs: object) -> http.client.HTTPConnection:
            return _PinnedHTTPS(host, pinned, connector, tls, **kwargs)

        return self.do_open(factory, req)  # type: ignore[arg-type,no-any-return]


def guarded_opener(
    resolve: Resolver = system_resolver, connector: Connector = system_connector
) -> urllib.request.OpenerDirector:
    """An opener that reads only http and https, through the checks above. Redirects are
    followed by urllib's own handler, and each hop comes back through the guarded handlers.
    Only these handlers are installed, so file:, ftp: and data: have no handler at all."""
    opener = urllib.request.OpenerDirector()
    for handler in (
        urllib.request.ProxyHandler({}),
        _GuardedHTTP(resolve, connector),
        _GuardedHTTPS(resolve, connector),
        urllib.request.HTTPRedirectHandler(),
        urllib.request.HTTPErrorProcessor(),
        urllib.request.HTTPDefaultErrorHandler(),
    ):
        opener.add_handler(handler)
    return opener


def describe_fetch_error(err: BaseException) -> str:
    """The text the tool shows for a failed fetch. Refusals keep their own words."""
    if isinstance(err, Refused):
        return str(err)
    if isinstance(err, urllib.error.HTTPError):
        return f"the page answered {err.code}"
    if isinstance(err, urllib.error.URLError):
        reason = err.reason
        if isinstance(reason, Refused):
            return str(reason)
        return f"the page could not be fetched: {reason}"
    return f"the page could not be fetched: {err}"
