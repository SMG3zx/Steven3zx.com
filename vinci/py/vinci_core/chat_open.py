"""The open tool's output for the chat. What `vinci open` prints for each fact of an id, joined,
then cut to OPEN_TOTAL_BYTES. The cut goes at a line boundary when there is one, and the notice says
how many bytes were not shown and what to open next. The notice is counted in the cap.

Port of mojo/src/chat_open.mojo.
"""

from __future__ import annotations

from .handles import Stored
from .open_view import Request, build_view, render_text

OPEN_TOTAL_BYTES = 4000


def notice_for(hidden: int, ident: str, multi: bool) -> str:
    """The read-on notice. It names grep for every id and lines= for one fact."""
    more = "open " + ident + " grep=<word>"
    if not multi:
        more += " or open " + ident + " lines=A-B"
    return (
        "(output cut at " + str(OPEN_TOTAL_BYTES) + " bytes, " + str(hidden)
        + " bytes not shown; read on with " + more + ")"
    )


def boundary_cut(text: str, allowed: int) -> str:
    """The longest prefix of text within allowed bytes that ends at a line boundary, or the prefix
    cut inside a line when the first line alone is longer."""
    if allowed <= 0:
        return ""
    head = text.encode("utf-8")[:allowed].decode("utf-8", errors="ignore")
    last = head.encode("utf-8").rfind(b"\n")
    if last <= 0:
        return head
    return head.encode("utf-8")[:last].decode("utf-8")


def cap_output(text: str, ident: str, multi: bool) -> str:
    """The text, cut to OPEN_TOTAL_BYTES with the notice included in that count. The notice is
    sized for the whole text, so it is never longer than the one that is printed."""
    total = len(text.encode("utf-8"))
    if total <= OPEN_TOTAL_BYTES:
        return text
    sized = notice_for(total, ident, multi)
    kept = boundary_cut(text, OPEN_TOTAL_BYTES - len(sized.encode("utf-8")) - 1)
    notice = notice_for(total - len(kept.encode("utf-8")), ident, multi)
    return kept + "\n" + notice


def open_text(items: list[Stored], ident: str, req: Request) -> str:
    """The views of every fact an id names, each rendered as `vinci open` renders it, joined with
    a blank line, then capped. Raises as build_view does (a lines= range past the end)."""
    out = ""
    for i, item in enumerate(items):
        view = build_view(ident, item, req, OPEN_TOTAL_BYTES)
        if i > 0:
            out += "\n\n"
        out += render_text(view)
    return cap_output(out, ident, len(items) > 1)
