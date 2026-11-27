"""The daily brief: open commitments, the review's findings, what was recorded in the last
week, and time per idea. Deterministic: the same journal and the same time give the same text.

Every line cites its fact. A fact with an id is cited as [id]. A fact with no id (a commitment,
a commit) is cited as [kind day]. Time per idea is totals only: window titles stay in the
journal, reachable through recall and open.
"""

from __future__ import annotations

from .journal import Event
from .review import findings
from .state import State
from .text import clip, day, span
from .timeutil import TICKS_PER_DAY, TICKS_PER_MINUTE, Instant

WINDOW = 7 * TICKS_PER_DAY
FINDINGS_SHOWN = 5
RECENT_SHOWN = 10
RECENT_WIDTH = 80

# Facts the tracker and the chat write for their own bookkeeping, not things the person recorded.
BOOKKEEPING = frozenset(["spent", "work", "sync", "exchange", "memory", "review"])


def cite(kind: str, at: Instant, fact_id: str | None) -> str:
    if fact_id:
        return "[" + fact_id + "]"
    return "[" + kind + " " + day(at) + "]"


def within_week(at: Instant, now: Instant) -> bool:
    ago = now.utc - at.utc
    return 0 <= ago <= WINDOW


def brief(events: list[Event], state: State, now: Instant) -> str:
    lines: list[str] = ["Brief for " + day(now)]

    lines.append("")
    lines.append("Commitments")
    open_index = state.open_commitment_index()
    if open_index >= 0:
        c = state.commitments[open_index]
        lines.append("  open: " + c.text + " " + cite("commit", c.at, None))
    else:
        lines.append("  none open")

    lines.append("")
    lines.append("Review")
    found = findings(state, now)
    if not found:
        lines.append("  nothing found")
    for f in found[:FINDINGS_SHOWN]:
        lines.append("  " + f.problem + " [" + f.subject + "]")

    lines.append("")
    lines.append("Recorded this week")
    recent = [
        e
        for e in events
        if e.kind not in BOOKKEEPING and within_week(e.at, now) and (e.text or e.id)
    ]
    recent.sort(key=lambda e: e.at.utc, reverse=True)
    if not recent:
        lines.append("  nothing recorded")
    for e in recent[:RECENT_SHOWN]:
        body = e.text if e.text else e.id or ""
        lines.append(
            "  " + day(e.at) + " " + e.kind + ": " + clip(body, RECENT_WIDTH) + " "
            + cite(e.kind, e.at, e.id)
        )

    lines.append("")
    lines.append("Time this week")
    minutes: dict[str, float] = {}
    for e in events:
        if e.kind == "spent" and e.id and e.value is not None and within_week(e.at, now):
            minutes[e.id] = minutes.get(e.id, 0.0) + e.value
    if not minutes:
        lines.append("  nothing counted")
    titles = {i.id: i.title for i in state.ideas}
    for idea_id, total in sorted(minutes.items(), key=lambda kv: -kv[1]):
        name = titles.get(idea_id, idea_id)
        ticks = int(round(total * TICKS_PER_MINUTE))
        lines.append("  " + name + ": " + span(ticks) + " [" + idea_id + "]")

    return "\n".join(lines) + "\n"
