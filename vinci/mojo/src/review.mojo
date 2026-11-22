# The review loop: what needs fixing now, the one thing to do next, and where ideas
# sit longest. Nothing here prints; callers format the result.
#
# Public API (for the ideas agent's pipeline and next commands, and for cmd_ops):
#
#   comptime BUILD_LIMIT = 3                 ideas that may be in Build at once
#   comptime COMMITMENT_LENGTH = 7 days     in ticks; a commit is due
#                                            at now.add_ticks(COMMITMENT_LENGTH)
#
#   struct Finding(rank, subject, problem, fix)
#   def findings(state: State, now: Instant) raises -> List[Finding]
#       Every defect, lowest rank first. Ranks are 1 to 10. Within a
#       rank the order is: commitment, ideas, issues, principles,
#       predictions, metrics, notes.
#
#   struct NextStep(what, how)
#   def next_step(state: State, now: Instant) raises -> NextStep
#       The worst finding, else the idea in Build that waited longest,
#       else the early idea that was touched longest ago, else the Tend idea whose
#       goal is unmet, else "nothing is waiting on you". `what` and `how` are the two
#       lines `Next:` and `      ` print.
#
#   struct SlowestStage(stage, average)     average is in ticks
#   def slowest_stage(state: State, now: Instant) raises -> Optional[SlowestStage]
#       The stage of Seed, Prepare or Build where ideas sit
#       longest on average, counting every idea that ever entered it. None when no
#       idea has entered one of the three. The caller prints it when average is at
#       least TICKS_PER_HOUR. Ties keep the earlier stage.
#
# Stages are the Int codes from state.mojo (SEED, PREPARE, INCUBATE, BUILD, TEND,
# RETIRED). Text pieces use text.mojo (span, clip, day, num), so the output is laid out the same everywhere.

from state import BUILD, INCUBATE, PREPARE, RETIRED, SEED, TEND, State
from text import clip, day, num, span
from timeutil import Instant, TICKS_PER_DAY

# Limits. Every one is a first guess; change one when a logged issue says so.
comptime BUILDING_IDLE_LIMIT = 14 * TICKS_PER_DAY
comptime TENDING_IDLE_LIMIT = 30 * TICKS_PER_DAY
comptime UNUSED_LIMIT = 14 * TICKS_PER_DAY
comptime PREDICTION_LIMIT = 30 * TICKS_PER_DAY
comptime COMMITMENT_LENGTH = 7 * TICKS_PER_DAY
comptime MIN_POINTS_FOR_CONTROL = 5
comptime BREAKS_BEFORE_RETHINK = 2

# How many ideas may be in Build at once. More than this and none of them ships.
comptime BUILD_LIMIT = 3

# First bytes of the refs that count as links out of a note (r[0] is 'n', 'i' or 'h').
comptime BYTE_N = 110  # 'n'
comptime BYTE_I = 105  # 'i'
comptime BYTE_H = 104  # 'h'


@fieldwise_init
struct Finding(Copyable, Movable):
    """Something the system found wrong, and the command that fixes it."""

    var rank: Int
    var subject: String
    var problem: String
    var fix: String


@fieldwise_init
struct NextStep(Copyable, Movable):
    """The one thing to do now: what to do, and the command that does it."""

    var what: String
    var how: String


@fieldwise_init
struct SlowestStage(Copyable, Movable):
    """A stage, and the average time ideas have spent in it, in ticks."""

    var stage: Int
    var average: Int


@fieldwise_init
struct Limits(Copyable, Movable):
    """Shewhart's control limits for single measurements (Stats.Limits)."""

    var center: Float64
    var lower: Float64
    var upper: Float64

    def holds(self, x: Float64) -> Bool:
        return x >= self.lower and x <= self.upper


def control_limits(xs: List[Float64]) -> Optional[Limits]:
    """Stats.ControlLimits: the mean, plus or minus 2.66 times the average step
    between neighbours. None with fewer than two points.

    Kept here rather than imported, so this module stands alone.
    """
    if len(xs) < 2:
        return None
    var total = 0.0
    for x in xs:
        total += x
    var center = total / Float64(len(xs))
    var step_total = 0.0
    for k in range(1, len(xs)):
        var step = xs[k] - xs[k - 1]
        if step < 0.0:
            step = -step
        step_total += step
    var average_step = step_total / Float64(len(xs) - 1)
    return Limits(
        center,
        center - 2.66 * average_step,
        center + 2.66 * average_step,
    )


def contains_text(items: List[String], want: String) -> Bool:
    """HashSet.Contains for strings, by a scan."""
    for item in items:
        if item == want:
            return True
    return False


def links_out(refs: List[String]) raises -> Bool:
    """Whether any reference starts with 'n', 'i' or 'h'. An empty reference raises.
    """
    for r in refs:
        var b = r.as_bytes()
        if len(b) == 0:
            raise Error("journal: empty reference")
        if Int(b[0]) == BYTE_N or Int(b[0]) == BYTE_I or Int(b[0]) == BYTE_H:
            return True
    return False


def live_by_touch(state: State) -> List[Int]:
    """Indices of the ideas not retired, by last touch, oldest first. A stable
    sort: equal times keep their journal order."""
    var order = List[Int]()
    for i in range(len(state.ideas)):
        if state.ideas[i].stage != RETIRED:
            order.append(i)
    for a in range(1, len(order)):
        var j = a
        while (
            j > 0
            and state.ideas[order[j - 1]].last_touched.utc
            > state.ideas[order[j]].last_touched.utc
        ):
            var tmp = order[j]
            order[j] = order[j - 1]
            order[j - 1] = tmp
            j -= 1
    return order^


def findings(state: State, now: Instant) raises -> List[Finding]:
    """Every defect, lowest rank first, in a fixed order within a rank."""
    var found = List[Finding]()

    var open_index = state.open_commitment_index()
    if open_index >= 0:
        var due = (
            now.utc - state.commitments[open_index].at.utc >= COMMITMENT_LENGTH
        )
        if due:
            found.append(
                Finding(
                    1,
                    "week",
                    "commitment from "
                    + day(state.commitments[open_index].at)
                    + " is due: "
                    + clip(state.commitments[open_index].text, 50),
                    'vinci kept, or vinci missed "why"',
                )
            )
    else:
        var any_live = False
        for i in range(len(state.ideas)):
            if state.ideas[i].stage != RETIRED:
                any_live = True
        if any_live:
            found.append(
                Finding(
                    1,
                    "week",
                    "no commitment for this week",
                    'vinci commit "the one thing I will get done" [idea]',
                )
            )

    for i in range(len(state.ideas)):
        var stage = state.ideas[i].stage
        var id = state.ideas[i].id
        var title = state.ideas[i].title
        if stage == RETIRED:
            continue

        if stage == INCUBATE:
            if state.ideas[i].incubate_until is not None:
                if now.utc >= state.ideas[i].incubate_until.value().utc:
                    found.append(
                        Finding(
                            3,
                            id,
                            "incubation is over: " + title,
                            "vinci advance " + id,
                        )
                    )
            continue

        if stage == TEND and len(state.ideas[i].uses) == 0:
            if len(state.ideas[i].ships) == 0:
                raise Error("journal: " + id + " is in Tend but never shipped")
            var since = now.utc - state.ideas[i].ships[0].at.utc
            if since > UNUSED_LIMIT:
                found.append(
                    Finding(
                        4,
                        id,
                        "shipped "
                        + span(since)
                        + " ago and never used: "
                        + title,
                        "use it and vinci used "
                        + id
                        + ", or vinci retire "
                        + id
                        + ' "why"',
                    )
                )

        if stage == BUILD and not state.watched(i):
            found.append(
                Finding(
                    3,
                    id,
                    "has no home, so its time is not counted: " + title,
                    "vinci home "
                    + id
                    + " <folder>, or vinci watch "
                    + id
                    + ' "text in its window titles"',
                )
            )

        var idle = now.utc - state.ideas[i].last_touched.utc
        var limit = TENDING_IDLE_LIMIT if stage == TEND else BUILDING_IDLE_LIMIT
        if idle > limit:
            found.append(
                Finding(
                    6,
                    id,
                    "untouched for " + span(idle) + ": " + title,
                    "work on it, vinci tend "
                    + id
                    + ' "...", or vinci retire '
                    + id
                    + ' "why"',
                )
            )

    for x in range(len(state.issues)):
        if state.issues[x].principle_id is not None:
            continue
        var big = state.issues[x].big
        var rank = 2 if big else 5
        var size = "big issue, " if big else ""
        var id = state.issues[x].id
        var text = clip(state.issues[x].text, 50)
        if state.issues[x].root_cause is None:
            found.append(
                Finding(
                    rank,
                    id,
                    size + "no root cause yet: " + text,
                    "vinci diagnose " + id + ' "root cause"',
                )
            )
        else:
            found.append(
                Finding(
                    rank,
                    id,
                    size + "diagnosed but nothing changed: " + text,
                    'vinci principle "what I do from now on" ' + id,
                )
            )

    for p in range(len(state.principles)):
        var breaks = state.breaks(state.principles[p].id)
        if breaks >= BREAKS_BEFORE_RETHINK:
            found.append(
                Finding(
                    7,
                    state.principles[p].id,
                    "broken "
                    + String(breaks)
                    + " times, so the rule is not working: "
                    + clip(state.principles[p].text, 50),
                    (
                        "a rule you keep breaking needs a change to the"
                        " process, not more willpower"
                    ),
                )
            )

    for h in range(len(state.predictions)):
        if state.predictions[h].right is not None:
            continue
        var age = now.utc - state.predictions[h].at.utc
        if age > PREDICTION_LIMIT:
            var id = state.predictions[h].id
            found.append(
                Finding(
                    8,
                    id,
                    "predicted "
                    + span(age)
                    + " ago and never tested: "
                    + clip(state.predictions[h].text, 50),
                    "run the test, then vinci result "
                    + id
                    + ' right|wrong "what happened"',
                )
            )

    for m in range(len(state.metrics)):
        if len(state.metrics[m].points) < MIN_POINTS_FOR_CONTROL:
            continue
        var values = List[Float64]()
        for p in state.metrics[m].points:
            values.append(p.value)
        var limits = control_limits(values)
        if limits is None:
            continue
        var last = values[len(values) - 1]
        if not limits.value().holds(last):
            found.append(
                Finding(
                    9,
                    state.metrics[m].name,
                    "out of control: last value "
                    + num(last)
                    + " is outside "
                    + num(limits.value().lower)
                    + ".."
                    + num(limits.value().upper),
                    'find the special cause, then vinci issue "..."',
                )
            )

    var linked_to = List[String]()
    for n in range(len(state.notes)):
        for r in state.notes[n].refs:
            linked_to.append(r)
    for n in range(len(state.notes)):
        var id = state.notes[n].id
        if not links_out(state.notes[n].refs) and not contains_text(
            linked_to, id
        ):
            found.append(
                Finding(
                    10,
                    id,
                    "note connects to nothing: "
                    + clip(state.notes[n].text, 50),
                    "vinci link " + id + " <note or idea>",
                )
            )

    # Bucket by rank, which keeps the order within each rank. Ranks are 1 to 10.
    var out = List[Finding]()
    for rank in range(1, 11):
        for f in found:
            if f.rank == rank:
                out.append(f.copy())
    return out^


def next_step(state: State, now: Instant) raises -> NextStep:
    """Review.Next: the worst finding, else the Build idea that waited longest,
    else the early idea touched longest ago, else a Tend idea with an unmet goal.
    """
    var found = findings(state, now)
    if len(found) > 0:
        return NextStep(
            found[0].subject + ": " + found[0].problem, found[0].fix
        )

    var order = live_by_touch(state)
    for k in order:
        if state.ideas[k].stage == BUILD:
            var id = state.ideas[k].id
            return NextStep(
                "build " + id + ": " + state.ideas[k].title,
                "vinci ship " + id + ' "what shipped"',
            )
    for k in order:
        var stage = state.ideas[k].stage
        if stage == PREPARE or stage == SEED:
            var id = state.ideas[k].id
            return NextStep(
                "move " + id + " forward: " + state.ideas[k].title,
                "vinci advance " + id,
            )
    for k in order:
        if state.ideas[k].stage == TEND:
            var met = state.goal_met(k)
            if met is None or not met.value():
                var id = state.ideas[k].id
                var metric = state.ideas[k].goal_metric.copy().or_else("")
                return NextStep(
                    id + " has not met its goal: " + state.ideas[k].title,
                    "vinci log " + metric + " <value>",
                )
    return NextStep("nothing is waiting on you", 'vinci idea "title"')


def slowest_stage(state: State, now: Instant) raises -> Optional[SlowestStage]:
    """The slowest of Seed, Prepare and Build. Every idea that ever entered a stage
    counts, retired ones included.

    The average is computed in seconds, then cut to whole ticks. The conversion may
    round differently at the sub-millisecond level, which no span shows.
    """
    var best_stage = -1
    var best_average = 0
    var stages = List[Int]()
    stages.append(SEED)
    stages.append(PREPARE)
    stages.append(BUILD)
    for stage in stages:
        var count = 0
        var total_seconds = 0.0
        for i in range(len(state.ideas)):
            if state.ideas[i].entered(stage):
                count += 1
                total_seconds += (
                    Float64(state.ideas[i].time_in(stage, now)) / 10000000.0
                )
        if count == 0:
            continue
        var average_seconds = total_seconds / Float64(count)
        var average = Int(average_seconds * 10000000.0)
        if best_stage < 0 or average > best_average:
            best_stage = stage
            best_average = average
    if best_stage < 0:
        return None
    return SlowestStage(best_stage, best_average)
