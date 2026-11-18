# The open tool's output for the chat (M4 plan: open takes lines= or grep=, and one call's whole
# output is capped). What `vinci open` prints for each fact of an id, joined, then cut to
# OPEN_TOTAL_BYTES. The cut goes at a line boundary when there is one, and the notice says how
# many bytes were not shown and what to open next. The notice is counted in the cap.

from handles import Stored
from open_view import Request, build_view, render_text, utf8_prefix

comptime OPEN_TOTAL_BYTES = 4000


def notice_for(hidden: Int, id: String, multi: Bool) raises -> String:
    """The read-on notice. It names grep for every id and lines= for one fact.
    """
    var more = "open " + id + " grep=<word>"
    if not multi:
        more += " or open " + id + " lines=A-B"
    return (
        "(output cut at "
        + String(OPEN_TOTAL_BYTES)
        + " bytes, "
        + String(hidden)
        + " bytes not shown; read on with "
        + more
        + ")"
    )


def boundary_cut(text: String, allowed: Int) raises -> String:
    """The longest prefix of text within allowed bytes that ends at a line boundary, or the
    prefix cut inside a line when the first line alone is longer."""
    if allowed <= 0:
        return String()
    var head = utf8_prefix(text, allowed)
    var b = head.as_bytes()
    var last = -1
    for i in range(len(b)):
        if Int(b[i]) == 10:
            last = i
    if last <= 0:
        return head^
    var out = List[UInt8]()
    for i in range(last):
        out.append(b[i])
    return String(unsafe_from_utf8=out)


def cap_output(text: String, id: String, multi: Bool) raises -> String:
    """The text, cut to OPEN_TOTAL_BYTES with the notice included in that count. The notice
    is sized for the whole text, so it is never longer than the one that is printed.
    """
    var total = text.byte_length()
    if total <= OPEN_TOTAL_BYTES:
        return text.copy()
    var sized = notice_for(total, id, multi)
    var kept = boundary_cut(text, OPEN_TOTAL_BYTES - sized.byte_length() - 1)
    var notice = notice_for(total - kept.byte_length(), id, multi)
    return kept + "\n" + notice


def open_text(
    items: List[Stored],
    id: String,
    req: Request,
) raises -> String:
    """The views of every fact an id names, each rendered as `vinci open` renders it, joined
    with a blank line, then capped. Raises as build_view does (a lines= range past the end).
    """
    var out = String()
    for i in range(len(items)):
        var item = items[i].copy()
        var view = build_view(id, item, req, OPEN_TOTAL_BYTES)
        if i > 0:
            out += "\n\n"
        out += render_text(view)
    return cap_output(out, id, len(items) > 1)
