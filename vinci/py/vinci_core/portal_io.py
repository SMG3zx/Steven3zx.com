"""The portal's Python edge, as the port calls it: the calls that touch sockets, processes and the
file system's text decoding. Each function is one call into vinci_edge.portal_edge. Nothing here
decides what a page says.

Port of mojo/src/portal_io.mojo.
"""

from __future__ import annotations

from vinci_edge import portal_edge


def lower_invariant(text: str) -> str:
    """The text lowercased with invariant (culture-free) rules."""
    return portal_edge.lower_invariant(text)


def clock() -> float:
    return portal_edge.clock()


def absolute_path(path: str) -> str:
    return portal_edge.absolute_path(path)


def read_text_lenient(path: str) -> str:
    return portal_edge.read_text_lenient(path)


def git_portal_files(repository: str) -> str:
    return portal_edge.git_portal_files(repository)


def start_capture(url: str, draft: str, final: str, chromium: str) -> None:
    portal_edge.start_capture(url, draft, final, chromium)


def listen(port: int):  # type: ignore[no-untyped-def]
    """Bind the loopback addresses. Raises when the port is taken."""
    return portal_edge.listen(port)


def next_request(listener):  # type: ignore[no-untyped-def]
    return portal_edge.next_request(listener)


def start_probe(ports: list[int]):  # type: ignore[no-untyped-def]
    return portal_edge.start_probe(list(ports))
