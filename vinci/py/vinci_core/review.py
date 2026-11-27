"""The review loop: what needs fixing now, the one thing to do next, and where ideas sit longest.

Nothing here prints; callers format the result.

Port of mojo/src/review.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from .state import BUILD, INCUBATE, PREPARE, RETIRED, SEED, TEND, State
from .stats import control_limits
from .text import clip, day, num, span
from .timeutil import TICKS_PER_DAY, Instant

# Limits. Every one is a first guess; change one when a logged issue says so.
BUILDING_IDLE_LIMIT = 14 * TICKS_PER_DAY
TENDING_IDLE_LIMIT = 30 * TICKS_PER_DAY
UNUSED_LIMIT = 14 * TICKS_PER_DAY
PREDICTION_LIMIT = 30 * TICKS_PER_DAY
COMMITMENT_LENGTH = 7 * TICKS_PER_DAY
MIN_POINTS_FOR_CONTROL = 5
BREAKS_BEFORE_RETHINK = 2

# How many ideas may be in Build at once. More than this and none of them ships.
BUILD_LIMIT = 3


@dataclass
class Finding:
    """Something the system found wrong, and the command that fixes it."""

    rank: int
    subject: str
    problem: str
    fix: str


@dataclass
class NextStep:
    """The one thing to do now: what to do, and the command that does it."""

    what: str
    how: str


@dataclass
class SlowestStage:
    """A stage, and the average time ideas have spent in it, in ticks."""

    stage: int
    average: int


def links_out(refs: list[str]) -> bool:
    """Whether any reference starts with 'n', 'i' or 'h'. An empty reference raises."""
    for r in refs:
        if len(r) == 0:
            raise ValueError("journal: empty reference")
        if r[0] in ("n", "i", "h"):
            return True
    return False


def live_by_touch(state: State) -> list[int]:
    """Indices of the ideas not retired, by last touch, oldest first. A stable sort: equal
    times keep their journal order."""
    order = [i for i in range(len(state.ideas)) if state.ideas[i].stage != RETIRED]
    order.sort(key=lambda i: state.ideas[i].last_touched.utc)
    return order


def findings(state: State, now: Instant) -> list[Finding]:
    """Every defect, lowest rank first, in a fixed order within a rank."""
    found: list[Finding] = []

    open_index = state.open_commitment_index()
    if open_index >= 0:
        due = now.utc - state.commitments[open_index].at.utc >= COMMITMENT_LENGTH
        if due:
            found.append(
                Finding(
                    1,
                    "week",
                    "commitment from "
                    + day(state.commitments[open_index].at)
                    + " is due: "
                    + clip(state.commitments[open_index].text, 50),
                    'vinci journal kept, or vinci journal missed "why"',
                )
            )
    else:
        any_live = any(i.stage != RETIRED for i in state.ideas)
        if any_live:
            found.append(
                Finding(
                    1,
                    "week",
                    "no commitment for this week",
                    'vinci journal commit "the one thing I will get done" [idea]',
                )
            )

    for idea in state.ideas:
        stage = idea.stage
        ident = idea.id
        title = idea.title
        if stage == RETIRED:
            continue

        if stage == INCUBATE:
            if idea.incubate_until is not None:
                if now.utc >= idea.incubate_until.utc:
                    found.append(Finding(3, ident, "incubation is over: " + title, "vinci journal advance " + ident))
            continue

        if stage == TEND and len(idea.uses) == 0:
            if len(idea.ships) == 0:
                raise ValueError("journal: " + ident + " is in Tend but never shipped")
            since = now.utc - idea.ships[0].at.utc
            if since > UNUSED_LIMIT:
                found.append(
                    Finding(
                        4,
                        ident,
                        "shipped " + span(since) + " ago and never used: " + title,
                        "use it and vinci journal used " + ident + ", or vinci journal retire " + ident + ' "why"',
                    )
                )

        if stage == BUILD and not state.watched(state.idea_index(ident)):
            found.append(
                Finding(
                    3,
                    ident,
                    "has no home, so its time is not counted: " + title,
                    "vinci journal home " + ident + " <folder>, or vinci journal watch " + ident + ' "text in its window titles"',
                )
            )

        idle = now.utc - idea.last_touched.utc
        limit = TENDING_IDLE_LIMIT if stage == TEND else BUILDING_IDLE_LIMIT
        if idle > limit:
            found.append(
                Finding(
                    6,
                    ident,
                    "untouched for " + span(idle) + ": " + title,
                    "work on it, vinci journal tend " + ident + ' "...", or vinci journal retire ' + ident + ' "why"',
                )
            )

    for x in state.issues:
        if x.principle_id is not None:
            continue
        big = x.big
        rank = 2 if big else 5
        size = "big issue, " if big else ""
        ident = x.id
        text = clip(x.text, 50)
        if x.root_cause is None:
            found.append(Finding(rank, ident, size + "no root cause yet: " + text, "vinci journal diagnose " + ident + ' "root cause"'))
        else:
            found.append(
                Finding(
                    rank,
                    ident,
                    size + "diagnosed but nothing changed: " + text,
                    'vinci journal principle "what I do from now on" ' + ident,
                )
            )

    for p in state.principles:
        breaks = state.breaks(p.id)
        if breaks >= BREAKS_BEFORE_RETHINK:
            found.append(
                Finding(
                    7,
                    p.id,
                    "broken " + str(breaks) + " times, so the rule is not working: " + clip(p.text, 50),
                    "a rule you keep breaking needs a change to the process, not more willpower",
                )
            )

    for h in state.predictions:
        if h.right is not None:
            continue
        age = now.utc - h.at.utc
        if age > PREDICTION_LIMIT:
            found.append(
                Finding(
                    8,
                    h.id,
                    "predicted " + span(age) + " ago and never tested: " + clip(h.text, 50),
                    "run the test, then vinci journal result " + h.id + ' right|wrong "what happened"',
                )
            )

    for m in state.metrics:
        if len(m.points) < MIN_POINTS_FOR_CONTROL:
            continue
        values = [p.value for p in m.points]
        limits = control_limits(values)
        if limits is None:
            continue
        last = values[-1]
        if not limits.holds(last):
            found.append(
                Finding(
                    9,
                    m.name,
                    "out of control: last value "
                    + num(last)
                    + " is outside "
                    + num(limits.lower)
                    + ".."
                    + num(limits.upper),
                    'find the special cause, then vinci journal issue "..."',
                )
            )

    linked_to: list[str] = []
    for n in state.notes:
        linked_to.extend(n.refs)
    for n in state.notes:
        if not links_out(n.refs) and n.id not in linked_to:
            found.append(
                Finding(
                    10,
                    n.id,
                    "note connects to nothing: " + clip(n.text, 50),
                    "vinci journal link " + n.id + " <note or idea>",
                )
            )

    # Bucket by rank, which keeps the order within each rank. Ranks are 1 to 10.
    out: list[Finding] = []
    for rank in range(1, 11):
        out.extend(f for f in found if f.rank == rank)
    return out


def next_step(state: State, now: Instant) -> NextStep:
    """Review.Next: the worst finding, else the Build idea that waited longest, else the
    early idea touched longest ago, else a Tend idea with an unmet goal."""
    found = findings(state, now)
    if len(found) > 0:
        return NextStep(found[0].subject + ": " + found[0].problem, found[0].fix)

    order = live_by_touch(state)
    for k in order:
        if state.ideas[k].stage == BUILD:
            ident = state.ideas[k].id
            return NextStep("build " + ident + ": " + state.ideas[k].title, 'vinci journal ship ' + ident + ' "what shipped"')
    for k in order:
        stage = state.ideas[k].stage
        if stage in (PREPARE, SEED):
            ident = state.ideas[k].id
            return NextStep("move " + ident + " forward: " + state.ideas[k].title, "vinci journal advance " + ident)
    for k in order:
        if state.ideas[k].stage == TEND:
            met = state.goal_met(k)
            if met is None or not met:
                ident = state.ideas[k].id
                metric = state.ideas[k].goal_metric or ""
                return NextStep(
                    ident + " has not met its goal: " + state.ideas[k].title,
                    "vinci journal log " + metric + " <value>",
                )
    return NextStep("nothing is waiting on you", 'vinci journal idea "title"')


def slowest_stage(state: State, now: Instant) -> Optional[SlowestStage]:
    """The slowest of Seed, Prepare and Build. Every idea that ever entered a stage counts,
    retired ones included.

    The average is computed in seconds, then cut to whole ticks.
    """
    best_stage = -1
    best_average = 0
    for stage in (SEED, PREPARE, BUILD):
        count = 0
        total_seconds = 0.0
        for idea in state.ideas:
            if idea.entered(stage):
                count += 1
                total_seconds += float(idea.time_in(stage, now)) / 10000000.0
        if count == 0:
            continue
        average_seconds = total_seconds / float(count)
        average = int(average_seconds * 10000000.0)
        if best_stage < 0 or average > best_average:
            best_stage = stage
            best_average = average
    if best_stage < 0:
        return None
    return SlowestStage(best_stage, best_average)
