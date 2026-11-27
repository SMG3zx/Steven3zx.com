"""One card on the portal page: a Link, with its screenshot name.

A saved link is named by the slug of its title; a discovered one by its port, because the title
of a page is whatever the page calls itself.

Port of mojo/src/portal_link.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from .portal_url import local_port_of
from vinci_edge.portal_edge import lower_invariant


@dataclass
class Link:
    title: str
    url: str
    description: Optional[str]
    tags: list[str]
    discovered: bool


def is_slug_char(c: int) -> bool:
    return (97 <= c <= 122) or (48 <= c <= 57)


def slug(title: str) -> str:
    """Lowercase, every run of other characters becomes one dash, and the dashes at both ends
    go. "Qwik Docs" becomes "qwik-docs"."""
    lowered = lower_invariant(title)
    out = bytearray()
    pending_dash = False
    for c in lowered.encode("utf-8"):
        if is_slug_char(c):
            if pending_dash and len(out) > 0:
                out.append(45)
            pending_dash = False
            out.append(c)
        else:
            pending_dash = True
    return out.decode("ascii")


def preview_of(link: Link) -> str:
    """The screenshot name: local-<port> for a discovered page, else the slug of the title."""
    if link.discovered:
        port = local_port_of(link.url)
        return "local-" + str(port)
    return slug(link.title)
