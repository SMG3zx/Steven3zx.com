"""The ideas group, from inception to grown.

Owns: idea, for, goal, req, cut, advance, adopt, home, watch, unwatch, watches, spent,
ship, used, tend, retire, ideas, pipeline, next, and the idea part of show (show_idea).

Inputs that would break the journal after an event is appended (a span of minutes past the
duration range, a day count past the date range) are user errors, raised before anything is
recorded.

Port of mojo/src/cmd_ideas.mojo.
"""

from __future__ import annotations

import os
from typing import Optional

from .journal import Event
from .review import BUILD_LIMIT, next_step, slowest_stage
from .session import Session, note_line, number, prediction_line, user_error
from .state import (
    BUILD,
    INCUBATE,
    PREPARE,
    RETIRED,
    SEED,
    TEND,
    Issue,
    equals_ignore_case,
    file_name,
    home_pattern,
    stage_name,
)
from .text import day, decimal_digits, fixed_text, num, pad_right, round_half_up, span
from .timeutil import TICKS_PER_DAY, TICKS_PER_HOUR, TICKS_PER_MINUTE
from .track import NOTHING_WATCHED

GOAL_USAGE = 'goal <idea> "goal" <metric> at-least <n> | at-most <n> | between <low> <high>'
TICKS_MAX = 9223372036854775807
# 2^63 as a float: a span of minutes at or past it overflows.
TICKS_OVERFLOW_F = 9223372036854775808.0
DATE_TICKS_MAX_F = 3155378975999999999.0


def dispatch_ideas(s: Session, cmd: str, args: list[str]) -> bool:
    handled = True
    if cmd == "idea":
        seed_idea(s, s.arg(args, 0, "title"))
    elif cmd == "for":
        idx = idea_arg_index(s, args)
        set_for(s, idx, s.arg(args, 1, "who will use it"))
    elif cmd == "goal":
        idx = idea_arg_index(s, args)
        set_goal(s, idx, args)
    elif cmd == "req":
        idx = idea_arg_index(s, args)
        text = s.arg(args, 1, "requirement")
        who = s.arg(args, 2, "who asked, by name")
        why = s.arg(args, 3, "why")
        add_req(s, idx, text, who, why)
    elif cmd == "cut":
        ident = s.arg(args, 0, "requirement id")
        cut_req(s, ident, s.arg(args, 1, "why"))
    elif cmd == "advance":
        idx = idea_arg_index(s, args)
        days = 1.0
        if len(args) > 1:
            days = number(args[1], "days")
        advance(s, idx, days)
    elif cmd == "adopt":
        adopt(s, s.arg(args, 0, "folder"))
    elif cmd == "home":
        idx = idea_arg_index(s, args)
        home(s, idx, s.arg(args, 1, "folder"))
    elif cmd == "watch":
        idx = idea_arg_index(s, args)
        pattern = s.arg(args, 1, "text in its window titles")
        kind = args[2] if len(args) > 2 else None
        watch(s, idx, pattern, kind)
    elif cmd == "unwatch":
        idx = idea_arg_index(s, args)
        unwatch(s, idx, s.arg(args, 1, "watched text"))
    elif cmd == "watches":
        watches(s)
    elif cmd == "spent":
        idx = idea_arg_index(s, args)
        minutes = number(s.arg(args, 1, "minutes"), "minutes")
        spent(s, idx, minutes)
    elif cmd == "ship":
        idx = idea_arg_index(s, args)
        ship(s, idx, s.arg(args, 1, "what shipped"))
    elif cmd == "used":
        idx = idea_arg_index(s, args)
        how = s.arg(args, 1, "how it went") if len(args) > 1 else None
        used(s, idx, how)
    elif cmd == "tend":
        idx = idea_arg_index(s, args)
        tend(s, idx, s.arg(args, 1, "what you did"))
    elif cmd == "retire":
        idx = idea_arg_index(s, args)
        retire(s, idx, s.arg(args, 1, "why"))
    elif cmd == "ideas":
        ideas_list(s)
    elif cmd == "pipeline":
        pipeline(s)
    elif cmd == "next":
        next_action(s)
    else:
        handled = False
    return handled


# ---- helpers ----


def idea_arg_index(s: Session, a: list[str]) -> int:
    """IdeaArg: the first argument names an idea. Returns its index in the state."""
    ident = s.arg(a, 0, "idea id")
    i = s.state.idea_index(ident)
    if i < 0:
        raise user_error("no idea " + ident)
    return i


def idea_is(about: Optional[str], ident: str) -> bool:
    """True when an optional idea reference names ident."""
    return about is not None and about == ident


def join_items(items: list[str], sep: str) -> str:
    return sep.join(items)


def advise(text: str) -> None:
    """A gate is advice, not a lock. The move still happens."""
    print("Not ready: " + text)


def has_live_requirement(s: Session, ident: str) -> bool:
    return any(r.idea_id == ident and r.live() for r in s.state.requirements)


def has_prediction(s: Session, ident: str) -> bool:
    return any(idea_is(h.idea_id, ident) for h in s.state.predictions)


def remind(s: Session, stage: int) -> None:
    """A principle is shown again at the stage where its issue happened."""
    for p in s.state.principles:
        if p.stage is not None and p.stage == stage:
            print("Remember " + p.id + ": " + p.text)


def move_to(s: Session, idx: int, stage: int) -> None:
    """Record the stage change, say so, and remind the principles."""
    ident = s.state.ideas[idx].id
    s.record(Event(s.now, "stage", id=ident, text=stage_name(stage)))
    print(ident + " is now in " + stage_name(stage) + ".")
    remind(s, stage)


def goal_status(s: Session, idx: int) -> str:
    """The goal, and what the latest measurement says about it."""
    i = s.state.ideas[idx]
    if i.goal is None:
        return "no goal"
    metric = i.goal_metric or ""
    latest = ""
    m = s.state.metric_index(metric)
    if m >= 0 and len(s.state.metrics[m].points) > 0:
        latest = num(s.state.metrics[m].points[-1].value)
    met = s.state.goal_met(idx)
    if met is None:
        return metric + " not measured yet, needs " + i.goal_target()
    if met:
        return "met, " + metric + " " + latest + " is " + i.goal_target() + ". Raise it?"
    return "not met, " + metric + " " + latest + " needs " + i.goal_target()


def percent_one_decimal(ratio: float) -> str:
    """A percentage with one decimal: the ratio times 100, rounded half up from the 15
    significant digits of the float."""
    d = decimal_digits(ratio * 100.0)
    fifteen = round_half_up(d.digits, d.point, 15)
    one = round_half_up(fifteen.digits, fifteen.point, fifteen.point + 1)
    from .text import Digits

    one = Digits(d.negative, one.digits, one.point)
    text = fixed_text(one)
    if text.find(".") < 0:
        text += ".0"
    return text + "%"


def flow_text(s: Session, idx: int) -> str:
    """Touch time over elapsed time, or "-" when the idea is not yet old."""
    i = s.state.ideas[idx]
    age = s.now.utc - i.born.utc
    if age <= 0:
        return "-"
    return percent_one_decimal(float(i.touch) / float(age))


def issue_line(x: Issue) -> str:
    """Id, state (closed, diagnosed or open), BIG, and the text."""
    state_word = "open     "
    if x.principle_id is not None:
        state_word = "closed   "
    elif x.root_cause is not None:
        state_word = "diagnosed"
    big = "BIG " if x.big else ""
    return pad_right(x.id, 4) + " " + state_word + " " + big + x.text


def folder_arg(folder: str) -> str:
    """The full path of an existing folder. A relative path is taken from the working
    directory. There is no tilde expansion, so "~" is a folder name."""
    path = os.path.abspath(folder)
    if path.startswith("//"):
        path = path[1:]
    if not os.path.isdir(path):
        raise user_error(path + " is not a folder")
    return path


def home_owner(s: Session, path: str) -> int:
    """The first idea whose home is this folder, or -1."""
    for i, idea in enumerate(s.state.ideas):
        if idea.home is not None and idea.home == path:
            return i
    return -1


def check_days_in_range(s: Session, days: float) -> None:
    """A day count that takes the date past the range vinci keeps is refused before the
    event is appended, so nothing is recorded."""
    target = float(s.now.local_ticks()) + days * float(TICKS_PER_DAY)
    if target < 0.0 or target > DATE_TICKS_MAX_F:
        raise user_error("days takes the date out of the range vinci keeps")


# ---- ideas: inception to grown ----


def seed_idea(s: Session, title: str) -> None:
    """NewIdea: capture an idea as a Seed."""
    ident = s.next_id("i")
    s.record(Event(s.now, "idea", id=ident, text=title))
    print(ident + " seeded. Next: vinci for " + ident + ' "who will use it", then vinci goal ' + ident + " ...")


def set_for(s: Session, idx: int, who: str) -> None:
    """For: who will use the idea."""
    ident = s.state.ideas[idx].id
    s.record(Event(s.now, "for", id=ident, text=who))
    print(ident + " is for " + who + ".")


def set_goal(s: Session, idx: int, a: list[str]) -> None:
    """Goal: goal <idea> "text" <metric> at-least|at-most <n>, or between <low> <high>."""
    text = s.arg(a, 1, "goal")
    if len(a) < 5:
        raise user_error("a goal needs a number that says when it is met: " + GOAL_USAGE)
    metric = a[2]
    kind = a[3]
    min_v: Optional[float] = None
    max_v: Optional[float] = None
    if kind == "at-least":
        min_v = number(a[4], "target")
    elif kind == "at-most":
        max_v = number(a[4], "target")
    elif kind == "between":
        low = number(a[4], "low")
        high = number(s.arg(a, 5, "high"), "high")
        if low > high:
            raise user_error("low must not be above high")
        min_v = low
        max_v = high
    else:
        raise user_error("'" + kind + "' is not at-least, at-most or between: " + GOAL_USAGE)
    ident = s.state.ideas[idx].id
    s.record(Event(s.now, "goal", id=ident, text=text, value=min_v, hi=max_v, metric=metric))
    print(ident + " goal set. Record progress with: vinci log " + metric + " <value>")


def add_req(s: Session, idx: int, text: str, who: str, why: str) -> None:
    """Req: a requirement, who asked for it, and the reason."""
    ident = s.next_id("r")
    idea_id = s.state.ideas[idx].id
    s.record(Event(s.now, "req", id=ident, text=text, refs=[idea_id], who=who, why=why))
    print(ident + " added to " + idea_id + ". Can it be deleted? vinci cut " + ident + ' "why"')


def cut_req(s: Session, ident: str, why: str) -> None:
    """Delete a requirement, and say how many of its idea's are gone."""
    r = s.state.requirement_index(ident)
    if r < 0:
        raise user_error("no requirement " + ident)
    req = s.state.requirements[r]
    if not req.live():
        raise user_error(ident + " is already cut")
    s.record(Event(s.now, "cut", id=ident, text=why))
    total = 0
    deleted = 0
    for q in s.state.requirements:
        if q.idea_id == req.idea_id:
            total += 1
            if not q.live():
                deleted += 1
    print(
        ident
        + " cut. "
        + req.idea_id
        + " has deleted "
        + str(deleted + 1)
        + " of "
        + str(total)
        + " requirements."
    )


def advance(s: Session, idx: int, days: float) -> None:
    """Pass the next gate, or say why not. A gate is advice, not a lock."""
    idea = s.state.ideas[idx]
    ident = idea.id
    if idea.stage == SEED:
        if idea.for_ is None:
            advise(ident + " is for nobody yet. Name who will use it: vinci for " + ident + ' "..."')
        if idea.goal is None:
            advise(ident + " has no goal. Set one with a number: vinci goal " + ident + ' "..." <metric> at-least <n>')
        move_to(s, idx, PREPARE)
        print(
            'Next: gather notes (vinci note "..." ' + ident + "), question every requirement "
            "(vinci req), and say what you expect (vinci predict)."
        )

    elif idea.stage == PREPARE:
        if len(s.state.notes_about(ident)) == 0:
            advise(
                ident
                + " has no notes. Preparation is gathering what is known:"
                + ' vinci note "..." <source> '
                + ident
            )
        if not has_live_requirement(s, ident):
            advise(
                ident
                + " has no requirements. State what it must do, who says so, and why: vinci req "
                + ident
                + ' "..." "name" "why"'
            )
        if not has_prediction(s, ident):
            advise(
                ident
                + " has no prediction. Write down what you expect before you test it: vinci predict "
                + '"..." <percent> '
                + ident
            )
        if days < 0.0:
            raise user_error("days cannot be negative")
        check_days_in_range(s, days)
        s.record(Event(s.now, "stage", id=ident, text=stage_name(INCUBATE), value=days))
        print(ident + " is incubating until " + day(s.now.add_days(days)) + ". Work on something else.")

    elif idea.stage == INCUBATE:
        if idea.incubate_until is not None:
            if s.now.utc < idea.incubate_until.utc:
                advise(
                    ident
                    + " is incubating until "
                    + day(idea.incubate_until)
                    + ". Leave it alone."
                )
        building = [o.id for o in s.state.ideas if o.stage == BUILD]
        if len(building) >= BUILD_LIMIT:
            advise(
                str(BUILD_LIMIT)
                + " ideas are already in Build ("
                + join_items(building, ", ")
                + "). Ship or retire one first."
            )
        move_to(s, idx, BUILD)
        if s.state.watched(idx):
            print("Next: ship the smallest thing that works.")
        else:
            print("Next: vinci home " + ident + " <folder>, then ship the smallest thing that works.")

    elif idea.stage == BUILD:
        raise user_error("Build ends by shipping: vinci ship " + ident + ' "what shipped"')
    elif idea.stage == TEND:
        raise user_error(ident + " is grown. It is tended, not finished: vinci ship or vinci tend")
    else:
        raise user_error(ident + " is retired: " + (idea.retired_why or ""))


def ship(s: Session, idx: int, what: str) -> None:
    """Record something shipped. The idea enters Tend on its first ship."""
    idea = s.state.ideas[idx]
    ident = idea.id
    if not (idea.stage == BUILD or idea.stage == TEND):
        raise user_error(
            ident
            + " is in "
            + stage_name(idea.stage)
            + ". It ships from Build or Tend: vinci advance "
            + ident
        )
    first = len(idea.ships) == 0
    since = idea.born.utc
    if not first:
        since = idea.ships[-1].at.utc
    cycle = s.now.utc - since
    s.record(Event(s.now, "ship", id=ident, text=what))
    since_word = "inception" if first else "the last ship"
    print(
        ident
        + " shipped (#"
        + str(len(idea.ships) + 1)
        + "). Cycle time "
        + span(cycle)
        + " since "
        + since_word
        + "; touch time "
        + span(idea.touch)
        + " in total."
    )
    print("Goal: " + goal_status(s, idx) + ".")
    open_ids = [h.id for h in s.state.predictions if idea_is(h.idea_id, ident) and h.right is None]
    if len(open_ids) > 0:
        print("Predictions to settle: " + join_items(open_ids, ", "))
    if first:
        print(ident + " is now in Tend. Use it yourself: vinci used " + ident)
        remind(s, TEND)


def used(s: Session, idx: int, how: Optional[str]) -> None:
    """Record that the shipped thing was used, and how it went."""
    idea = s.state.ideas[idx]
    ident = idea.id
    if len(idea.ships) == 0:
        raise user_error(ident + " has not shipped, so there is nothing to use")
    if idea.stage == RETIRED:
        raise user_error(ident + " is retired")
    s.record(Event(s.now, "used", id=ident, text=how))
    print(
        ident
        + " used "
        + str(len(idea.uses) + 1)
        + ' times. Anything wrong with it? vinci issue "..." '
        + ident
    )


def tend(s: Session, idx: int, what: str) -> None:
    """Record upkeep on an idea."""
    idea = s.state.ideas[idx]
    ident = idea.id
    if idea.stage == RETIRED:
        raise user_error(ident + " is retired")
    s.record(Event(s.now, "tend", id=ident, text=what))
    print(ident + " tended.")


def retire(s: Session, idx: int, why: str) -> None:
    """End an idea on purpose. Its notes stay."""
    idea = s.state.ideas[idx]
    ident = idea.id
    if idea.stage == RETIRED:
        raise user_error(ident + " is already retired")
    s.record(Event(s.now, "retire", id=ident, text=why))
    print(ident + " retired on purpose. Its notes stay.")


def ideas_list(s: Session) -> None:
    """Every idea, with its stage, touch time, age and flow."""
    if len(s.state.ideas) == 0:
        print('No ideas yet. vinci idea "title"')
        return
    for idx, i in enumerate(s.state.ideas):
        age = s.now.utc - i.born.utc
        print(
            pad_right(i.id, 4)
            + " "
            + pad_right(stage_name(i.stage), 8)
            + " touch "
            + pad_right(span(i.touch), 7)
            + " age "
            + pad_right(span(age), 7)
            + " flow "
            + pad_right(flow_text(s, idx), 6)
            + " "
            + i.title
        )


def pipeline_detail(s: Session, idx: int, stage: int) -> str:
    """The text in brackets on one pipeline line, which depends on the stage."""
    i = s.state.ideas[idx]
    if stage == INCUBATE:
        if i.incubate_until is None:
            raise ValueError("journal: incubating idea has no date")
        return "until " + day(i.incubate_until)
    if stage == BUILD:
        return (
            span(i.time_in(BUILD, s.now))
            + " here, touch "
            + span(i.touch)
            + "; goal "
            + goal_status(s, idx)
        )
    if stage == TEND:
        return (
            "shipped "
            + str(len(i.ships))
            + ", used "
            + str(len(i.uses))
            + "; goal "
            + goal_status(s, idx)
        )
    return span(i.time_in(stage, s.now)) + " here"


def pipeline(s: Session) -> None:
    """Every live idea by stage, where ideas get stuck, and the one thing to do now."""
    for st in (SEED, PREPARE, INCUBATE, BUILD, TEND):
        here = [idx for idx, idea in enumerate(s.state.ideas) if idea.stage == st]
        label = stage_name(st)
        if st == BUILD:
            label = "Build " + str(len(here)) + "/" + str(BUILD_LIMIT)
        if len(here) == 0:
            print(pad_right(label, 10) + " -")
        for j, idx in enumerate(here):
            shown = label if j == 0 else ""
            i = s.state.ideas[idx]
            print(
                pad_right(shown, 10)
                + " "
                + pad_right(i.id, 4)
                + " "
                + i.title
                + "  ("
                + pipeline_detail(s, idx, st)
                + ")"
            )

    slow = slowest_stage(s.state, s.now)
    if slow is not None:
        if slow.average >= TICKS_PER_HOUR:
            print(
                "Slowest stage: "
                + stage_name(slow.stage)
                + ", "
                + span(slow.average)
                + " on average. Speed that one up first."
            )
    next_action(s)


def next_action(s: Session) -> None:
    """The one thing to do now, from review.py."""
    step = next_step(s.state, s.now)
    print("Next: " + step.what)
    print("      " + step.how)


def adopt(s: Session, folder: str) -> None:
    """An existing folder becomes an idea, named after it. Adopting it again does nothing,
    so a whole directory can be adopted in a loop."""
    path = folder_arg(folder)
    owner = home_owner(s, path)
    if owner >= 0:
        print(s.state.ideas[owner].id + " already lives in " + path + ".")
        return
    ident = s.next_id("i")
    s.record(Event(s.now, "idea", id=ident, text=file_name(path)))
    s.record(Event(s.now, "home", id=ident, text=path))
    print(ident + " adopted from " + path + ". Its windows and commits now count as work.")


def home(s: Session, idx: int, folder: str) -> None:
    """Where the idea lives. Its editor windows and commits count as work."""
    idea = s.state.ideas[idx]
    ident = idea.id
    if idea.stage == RETIRED:
        raise user_error(ident + " is retired")
    path = folder_arg(folder)
    owner = home_owner(s, path)
    if owner >= 0:
        raise user_error(s.state.ideas[owner].id + " already lives in " + path)
    s.record(Event(s.now, "home", id=ident, text=path))
    print(
        ident
        + " lives in "
        + path
        + '. Time in windows titled "'
        + home_pattern(path)
        + '" and commits there now count as work.'
    )


def watch(s: Session, idx: int, pattern: str, kind: Optional[str]) -> None:
    """Count time in windows whose app or title contains the text. With "use", such a window
    counts as using what was shipped."""
    idea = s.state.ideas[idx]
    ident = idea.id
    if idea.stage == RETIRED:
        raise user_error(ident + " is retired")
    is_use = False
    if kind is not None:
        if kind != "use":
            raise user_error("after the text comes \"use\" or nothing, got '" + kind + "'")
        is_use = True
    for w in s.state.all_watches():
        if equals_ignore_case(w.pattern, pattern):
            raise user_error('"' + pattern + '" is already watched for ' + w.idea_id)
    value: Optional[float] = 1.0 if is_use else None
    s.record(Event(s.now, "watch", id=ident, text=pattern, value=value))
    if is_use:
        print('A window whose app or title contains "' + pattern + '" now counts as using ' + ident + ".")
    else:
        print('Time in a window whose app or title contains "' + pattern + '" now counts for ' + ident + ".")


def unwatch(s: Session, idx: int, pattern: str) -> None:
    """Stop counting time for a pattern that was recorded for the idea."""
    ident = s.state.ideas[idx].id
    found = any(w.idea_id == ident and equals_ignore_case(w.pattern, pattern) for w in s.state.watches)
    if not found:
        raise user_error(ident + ' does not watch "' + pattern + '"')
    s.record(Event(s.now, "unwatch", id=ident, text=pattern))
    print(ident + ' no longer watches "' + pattern + '".')


def watches(s: Session) -> None:
    """What is watched, recorded or implied by a home folder."""
    all_w = s.state.all_watches()
    if len(all_w) == 0:
        print(NOTHING_WATCHED)
        return
    for w in all_w:
        title = s.state.ideas[s.state.idea_index(w.idea_id)].title
        mode = "use " if w.use else "work"
        print(pad_right(w.idea_id, 4) + " " + mode + ' "' + w.pattern + '"  ' + title)


def spent(s: Session, idx: int, minutes: float) -> None:
    """Add time spent away from the screen."""
    if minutes <= 0.0:
        raise user_error("minutes must be positive")
    if minutes * float(TICKS_PER_MINUTE) >= TICKS_OVERFLOW_F:
        raise user_error("minutes is past the longest span vinci keeps")
    idea = s.state.ideas[idx]
    ident = idea.id
    ticks = int(minutes * float(TICKS_PER_MINUTE))
    if ticks > TICKS_MAX - idea.touch:
        raise user_error(ident + " would pass the longest span vinci keeps")
    s.record(Event(s.now, "spent", id=ident, value=minutes))
    print(span(ticks) + " added to " + ident + ".")


def show_idea(s: Session, idea_index: int) -> None:
    """The idea's full view, with its requirements, predictions, notes, credits, ships and
    issues."""
    i = s.state.ideas[idea_index]
    now = s.now
    print(i.id + "  " + i.title)

    stage_line = "stage   " + stage_name(i.stage)
    if i.stage == INCUBATE and i.incubate_until is not None:
        stage_line += " until " + day(i.incubate_until)
    if i.retired_why is not None:
        stage_line += ": " + i.retired_why
    print(stage_line)

    for_text = i.for_ if i.for_ is not None else "(nobody yet)"
    print("for     " + for_text)

    goal_line = "goal    "
    if i.goal is None:
        goal_line += "(none)"
    else:
        goal_line += i.goal + "  [" + goal_status(s, idea_index) + "]"
    print(goal_line)

    print(
        "time    born "
        + day(i.born)
        + ", age "
        + span(now.utc - i.born.utc)
        + ", touch "
        + span(i.touch)
        + ", flow "
        + flow_text(s, idea_index)
    )

    if i.home is not None:
        home_line = "home    " + i.home
        n = len(i.work)
        if n > 0:
            plural = "" if n == 1 else "s"
            home_line += "  (" + str(n) + " commit" + plural + ", last " + day(i.work[n - 1].at) + ")"
        print(home_line)

    watch_items: list[str] = []
    for w in s.state.all_watches():
        if w.idea_id == i.id:
            item = '"' + w.pattern + '"'
            if w.use:
                item += " (use)"
            watch_items.append(item)
    if len(watch_items) > 0:
        print("watch   " + join_items(watch_items, ", "))

    stage_parts: list[str] = []
    for st in (SEED, PREPARE, INCUBATE, BUILD, TEND):
        if i.entered(st):
            part = stage_name(st) + " " + span(i.time_in(st, now))
            if i.touch_in[st] != 0:
                part += " (touch " + span(i.touch_in[st]) + ")"
            stage_parts.append(part)
    print("stages  " + join_items(stage_parts, ", "))

    if len(i.ships) > 0:
        uses = len(i.uses)
        used_line = "used    " + str(uses) + " times"
        if uses > 0:
            used_line += ", last " + day(i.uses[uses - 1])
        print(used_line)

    req_lines: list[str] = []
    for r in s.state.requirements:
        if r.idea_id == i.id:
            if r.live():
                req_lines.append(pad_right(r.id, 4) + " " + r.text + "  (" + r.who + ": " + r.why + ")")
            else:
                req_lines.append(pad_right(r.id, 4) + " CUT " + r.text + "  (" + (r.cut_why or "") + ")")
    s.section("requirements", req_lines)

    pred_lines = [prediction_line(h) for h in s.state.predictions if idea_is(h.idea_id, i.id)]
    s.section("predictions", pred_lines)

    about = s.state.notes_about(i.id)
    note_lines = [note_line(s.state.notes[n]) for n in about]
    s.section("notes", note_lines)

    credit_lines: list[str] = []
    seen: list[str] = []
    for n in about:
        for r in s.state.notes[n].refs:
            if r.startswith("s") and r not in seen:
                seen.append(r)
                k = s.state.source_index(r)
                if k < 0:
                    raise ValueError("journal: credit names a missing source: " + r)
                credit_lines.append(pad_right(s.state.sources[k].id, 4) + " " + s.state.sources[k].citation)
    s.section("credits", credit_lines)

    ship_lines = [day(sh.at) + "  " + sh.what for sh in i.ships]
    s.section("ships", ship_lines)

    issue_lines = [issue_line(x) for x in s.state.issues if idea_is(x.idea_id, i.id)]
    s.section("issues", issue_lines)
