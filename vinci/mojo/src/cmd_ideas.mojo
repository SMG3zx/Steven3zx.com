# The ideas group (App.cs "ideas: inception to grown"). Owns these `case` labels of
# Dispatch: idea, for, goal, req, cut, advance, adopt, home, watch, unwatch, watches,
# spent, ship, used, tend, retire, ideas, pipeline, next, and the idea part of show
# (show_idea, which cmd_knowledge's show calls).
#
# `args` is the list after the command name (C#'s `a`). Return True when the command
# was handled, False to let the next group try it.
#
# The review parts (pipeline's slowest stage line and next) call review.mojo.
#
# Inputs that make C# throw after it has appended an event (oracle-bugs/): a span of
# minutes past the TimeSpan range, a day count past the DateTime range. Here each one
# is a user error, raised before anything is recorded.

from std.python import Python, PythonObject

from journal import Event
from review import BUILD_LIMIT, next_step, slowest_stage
from session import Session, note_line, number, prediction_line, user_error
from state import (
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
from text import (
    ascii_tail,
    day,
    decimal_digits,
    fixed_text,
    num,
    pad_right,
    round_half_up,
    span,
)
from timeutil import TICKS_PER_DAY, TICKS_PER_HOUR, TICKS_PER_MINUTE

# Shown by watches when nothing is watched (Tracker.NothingWatched).
comptime NOTHING_WATCHED = (
    "Nothing is watched. vinci home <idea> <folder>, or vinci watch <idea>"
    ' "text in its window titles"'
)
comptime GOAL_USAGE = (
    'goal <idea> "goal" <metric> at-least <n> | at-most <n> | between <low>'
    " <high>"
)
# Int64.MaxValue, the most ticks a TimeSpan holds.
comptime TICKS_MAX = 9223372036854775807
# 2^63 as a Float64: a FromMinutes product at or past it overflows.
comptime TICKS_OVERFLOW_F = 9223372036854775808.0
# DateTime.MaxValue in ticks since 0001-01-01, and the same as a Float64.
comptime DATE_TICKS_MAX = 3155378975999999999
comptime DATE_TICKS_MAX_F = 3155378975999999999.0


def dispatch_ideas(
    mut s: Session, cmd: String, args: List[String]
) raises -> Bool:
    var handled = True
    if cmd == "idea":
        seed_idea(s, s.arg(args, 0, "title"))
    elif cmd == "for":
        var idx = idea_arg_index(s, args)
        set_for(s, idx, s.arg(args, 1, "who will use it"))
    elif cmd == "goal":
        var idx = idea_arg_index(s, args)
        set_goal(s, idx, args)
    elif cmd == "req":
        var idx = idea_arg_index(s, args)
        var text = s.arg(args, 1, "requirement")
        var who = s.arg(args, 2, "who asked, by name")
        var why = s.arg(args, 3, "why")
        add_req(s, idx, text, who, why)
    elif cmd == "cut":
        var id = s.arg(args, 0, "requirement id")
        cut_req(s, id, s.arg(args, 1, "why"))
    elif cmd == "advance":
        var idx = idea_arg_index(s, args)
        var days = Float64(1.0)
        if len(args) > 1:
            days = number(args[1], "days")
        advance(s, idx, days)
    elif cmd == "adopt":
        adopt(s, s.arg(args, 0, "folder"))
    elif cmd == "home":
        var idx = idea_arg_index(s, args)
        home(s, idx, s.arg(args, 1, "folder"))
    elif cmd == "watch":
        var idx = idea_arg_index(s, args)
        var pattern = s.arg(args, 1, "text in its window titles")
        var kind: Optional[String] = None
        if len(args) > 2:
            kind = Optional[String](args[2].copy())
        watch(s, idx, pattern, kind)
    elif cmd == "unwatch":
        var idx = idea_arg_index(s, args)
        unwatch(s, idx, s.arg(args, 1, "watched text"))
    elif cmd == "watches":
        watches(s)
    elif cmd == "spent":
        var idx = idea_arg_index(s, args)
        var minutes = number(s.arg(args, 1, "minutes"), "minutes")
        spent(s, idx, minutes)
    elif cmd == "ship":
        var idx = idea_arg_index(s, args)
        ship(s, idx, s.arg(args, 1, "what shipped"))
    elif cmd == "used":
        var idx = idea_arg_index(s, args)
        var how: Optional[String] = None
        if len(args) > 1:
            how = Optional[String](s.arg(args, 1, "how it went"))
        used(s, idx, how)
    elif cmd == "tend":
        var idx = idea_arg_index(s, args)
        tend(s, idx, s.arg(args, 1, "what you did"))
    elif cmd == "retire":
        var idx = idea_arg_index(s, args)
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


def idea_arg_index(s: Session, a: List[String]) raises -> Int:
    """IdeaArg: the first argument names an idea. Returns its index in the state.
    """
    var id = s.arg(a, 0, "idea id")
    var i = s.state.idea_index(id)
    if i < 0:
        raise user_error("no idea " + id)
    return i


def idea_is(about: Optional[String], id: String) -> Bool:
    """True when an optional idea reference names id."""
    return about is not None and about.value() == id


def join_items(items: List[String], sep: String) -> String:
    """String.Join(sep, items)."""
    var out = String()
    for k in range(len(items)):
        if k > 0:
            out += sep
        out += items[k]
    return out


def has_string(items: List[String], want: String) -> Bool:
    for item in items:
        if item == want:
            return True
    return False


def advise(text: String):
    """Advise: a gate is advice, not a lock. The move still happens."""
    print("Not ready: " + text)


def has_live_requirement(s: Session, id: String) -> Bool:
    for r in s.state.requirements:
        if r.idea_id == id and r.live():
            return True
    return False


def has_prediction(s: Session, id: String) -> Bool:
    for h in s.state.predictions:
        if idea_is(h.idea_id, id):
            return True
    return False


def remind(s: Session, stage: Int):
    """Remind: a principle is shown again at the stage where its issue happened.
    """
    for p in s.state.principles:
        if p.stage is not None and p.stage.value() == stage:
            print("Remember " + p.id + ": " + p.text)


def move_to(mut s: Session, idx: Int, stage: Int) raises:
    """MoveTo: record the stage change, say so, and remind the principles."""
    var id = s.state.ideas[idx].id
    s.record(
        Event(
            s.now.copy(),
            "stage",
            id=id.copy(),
            text=stage_name(stage),
        )
    )
    print(id + " is now in " + stage_name(stage) + ".")
    remind(s, stage)


def goal_status(s: Session, idx: Int) raises -> String:
    """GoalStatus: the goal, and what the latest measurement says about it."""
    var i = s.state.ideas[idx].copy()
    if i.goal is None:
        return "no goal"
    var metric = i.goal_metric.copy().or_else("")
    var latest = String("")
    var m = s.state.metric_index(metric)
    if m >= 0 and len(s.state.metrics[m].points) > 0:
        var last = len(s.state.metrics[m].points) - 1
        latest = num(s.state.metrics[m].points[last].value)
    var met = s.state.goal_met(idx)
    if met is None:
        return metric + " not measured yet, needs " + i.goal_target()
    if met.value():
        return (
            "met, "
            + metric
            + " "
            + latest
            + " is "
            + i.goal_target()
            + ". Raise it?"
        )
    return "not met, " + metric + " " + latest + " needs " + i.goal_target()


def percent_one_decimal(ratio: Float64) raises -> String:
    """ToString("0.0%") in invariant culture: the ratio times 100, one decimal,
    rounded half up from the 15 significant digits .NET formats with."""
    var d = decimal_digits(ratio * 100.0)
    var fifteen = round_half_up(d.digits, d.point, 15)
    var one = round_half_up(fifteen.digits, fifteen.point, fifteen.point + 1)
    one.negative = d.negative
    var text = fixed_text(one)
    if text.find(".") < 0:
        text += ".0"
    return text + "%"


def flow_text(s: Session, idx: Int) raises -> String:
    """Flow: touch time over elapsed time, or "-" when the idea is not yet old.
    """
    var i = s.state.ideas[idx].copy()
    var age = s.now.utc - i.born.utc
    if age <= 0:
        return "-"
    return percent_one_decimal(Float64(i.touch) / Float64(age))


def issue_line(x: Issue) raises -> String:
    """IssueLine: id, state (closed, diagnosed or open), BIG, and the text."""
    var state_word = String("open     ")
    if x.principle_id is not None:
        state_word = String("closed   ")
    elif x.root_cause is not None:
        state_word = String("diagnosed")
    var big = String("BIG ") if x.big else String()
    return pad_right(x.id, 4) + " " + state_word + " " + big + x.text


def folder_arg(folder: String) raises -> String:
    """FolderArg: the full path of an existing folder. A relative path is taken
    from the working directory, as Path.GetFullPath does. There is no tilde
    expansion, so "~" is a folder name, as it is in C#."""
    var os = Python.import_module("os")
    var path = String(os.path.abspath(PythonObject(folder)))
    if path.startswith("//"):
        path = ascii_tail(path, 1)
    if not Bool(os.path.isdir(PythonObject(path))):
        raise user_error(path + " is not a folder")
    return path


def home_owner(s: Session, path: String) -> Int:
    """The first idea whose home is this folder, or -1."""
    for i in range(len(s.state.ideas)):
        var home_path = s.state.ideas[i].home.copy()
        if home_path is not None and home_path.value() == path:
            return i
    return -1


def check_days_in_range(s: Session, days: Float64) raises:
    """AddDays throws past the DateTime range, after C# has already appended
    the event. Refuse first, so nothing is recorded."""
    var target = Float64(s.now.local_ticks()) + days * Float64(TICKS_PER_DAY)
    if target < 0.0 or target > DATE_TICKS_MAX_F:
        raise user_error("days takes the date out of the range vinci keeps")


# ---- ideas: inception to grown ----


def seed_idea(mut s: Session, title: String) raises:
    """NewIdea: capture an idea as a Seed."""
    var id = s.next_id("i")
    s.record(
        Event(
            s.now.copy(),
            "idea",
            id=id.copy(),
            text=title.copy(),
        )
    )
    print(
        id
        + " seeded. Next: vinci for "
        + id
        + ' "who will use it", then vinci goal '
        + id
        + " ..."
    )


def set_for(mut s: Session, idx: Int, who: String) raises:
    """For: who will use the idea."""
    var id = s.state.ideas[idx].id
    s.record(
        Event(
            s.now.copy(),
            "for",
            id=id.copy(),
            text=who.copy(),
        )
    )
    print(id + " is for " + who + ".")


def set_goal(mut s: Session, idx: Int, a: List[String]) raises:
    """Goal: goal <idea> "text" <metric> at-least|at-most <n>, or between <low> <high>.
    """
    var text = s.arg(a, 1, "goal")
    if len(a) < 5:
        raise user_error(
            "a goal needs a number that says when it is met: " + GOAL_USAGE
        )
    var metric = a[2].copy()
    var kind = a[3].copy()
    var min_v: Optional[Float64] = None
    var max_v: Optional[Float64] = None
    if kind == "at-least":
        min_v = Optional[Float64](number(a[4], "target"))
    elif kind == "at-most":
        max_v = Optional[Float64](number(a[4], "target"))
    elif kind == "between":
        var low = number(a[4], "low")
        var high = number(s.arg(a, 5, "high"), "high")
        if low > high:
            raise user_error("low must not be above high")
        min_v = Optional[Float64](low)
        max_v = Optional[Float64](high)
    else:
        raise user_error(
            "'" + kind + "' is not at-least, at-most or between: " + GOAL_USAGE
        )
    var id = s.state.ideas[idx].id
    s.record(
        Event(
            s.now.copy(),
            "goal",
            id=id.copy(),
            text=text.copy(),
            value=min_v^,
            hi=max_v^,
            metric=metric.copy(),
        )
    )
    print(
        id + " goal set. Record progress with: vinci log " + metric + " <value>"
    )


def add_req(
    mut s: Session, idx: Int, text: String, who: String, why: String
) raises:
    """Req: a requirement, who asked for it, and the reason."""
    var id = s.next_id("r")
    var idea_id = s.state.ideas[idx].id.copy()
    var refs = List[String]()
    refs.append(idea_id.copy())
    s.record(
        Event(
            s.now.copy(),
            "req",
            id=id.copy(),
            text=text.copy(),
            refs=Optional[List[String]](refs^),
            who=who.copy(),
            why=why.copy(),
        )
    )
    print(
        id
        + " added to "
        + idea_id
        + ". Can it be deleted? vinci cut "
        + id
        + ' "why"'
    )


def cut_req(mut s: Session, id: String, why: String) raises:
    """Cut: delete a requirement, and say how many of its idea's are gone."""
    var r = s.state.requirement_index(id)
    if r < 0:
        raise user_error("no requirement " + id)
    var req = s.state.requirements[r].copy()
    if not req.live():
        raise user_error(id + " is already cut")
    s.record(
        Event(
            s.now.copy(),
            "cut",
            id=id.copy(),
            text=why.copy(),
        )
    )
    var total = 0
    var deleted = 0
    for q in s.state.requirements:
        if q.idea_id == req.idea_id:
            total += 1
            if not q.live():
                deleted += 1
    print(
        id
        + " cut. "
        + req.idea_id
        + " has deleted "
        + String(deleted + 1)
        + " of "
        + String(total)
        + " requirements."
    )


def advance(mut s: Session, idx: Int, days: Float64) raises:
    """Advance: pass the next gate, or say why not. A gate is advice, not a lock.
    """
    var idea = s.state.ideas[idx].copy()
    var id = idea.id.copy()
    if idea.stage == SEED:
        if idea.for_ is None:
            advise(
                id
                + " is for nobody yet. Name who will use it: vinci for "
                + id
                + ' "..."'
            )
        if idea.goal is None:
            advise(
                id
                + " has no goal. Set one with a number: vinci goal "
                + id
                + ' "..." <metric> at-least <n>'
            )
        move_to(s, idx, PREPARE)
        print(
            'Next: gather notes (vinci note "..." '
            + id
            + "), question every requirement (vinci req), and say what you"
            + " expect (vinci predict)."
        )

    elif idea.stage == PREPARE:
        if len(s.state.notes_about(id)) == 0:
            advise(
                id
                + " has no notes. Preparation is gathering what is known:"
                + ' vinci note "..." <source> '
                + id
            )
        if not has_live_requirement(s, id):
            advise(
                id
                + " has no requirements. State what it must do, who says so,"
                + " and why: vinci req "
                + id
                + ' "..." "name" "why"'
            )
        if not has_prediction(s, id):
            advise(
                id
                + " has no prediction. Write down what you expect before you"
                + ' test it: vinci predict "..." <percent> '
                + id
            )
        if days < 0.0:
            raise user_error("days cannot be negative")
        check_days_in_range(s, days)
        s.record(
            Event(
                s.now.copy(),
                "stage",
                id=id.copy(),
                text=stage_name(INCUBATE),
                value=days,
            )
        )
        print(
            id
            + " is incubating until "
            + day(s.now.add_days(days))
            + ". Work on something else."
        )

    elif idea.stage == INCUBATE:
        if idea.incubate_until is not None:
            var until_utc = idea.incubate_until.value().utc
            if s.now.utc < until_utc:
                advise(
                    id
                    + " is incubating until "
                    + day(idea.incubate_until.value())
                    + ". Leave it alone."
                )
        var building = List[String]()
        for other in s.state.ideas:
            if other.stage == BUILD:
                building.append(other.id.copy())
        if len(building) >= BUILD_LIMIT:
            advise(
                String(BUILD_LIMIT)
                + " ideas are already in Build ("
                + join_items(building, ", ")
                + "). Ship or retire one first."
            )
        move_to(s, idx, BUILD)
        if s.state.watched(idx):
            print("Next: ship the smallest thing that works.")
        else:
            print(
                "Next: vinci home "
                + id
                + " <folder>, then ship the smallest thing that works."
            )

    elif idea.stage == BUILD:
        raise user_error(
            "Build ends by shipping: vinci ship " + id + ' "what shipped"'
        )
    elif idea.stage == TEND:
        raise user_error(
            id
            + " is grown. It is tended, not finished: vinci ship or vinci tend"
        )
    else:
        raise user_error(
            id + " is retired: " + idea.retired_why.copy().or_else("")
        )


def ship(mut s: Session, idx: Int, what: String) raises:
    """Ship: record something shipped. The idea enters Tend on its first ship.
    """
    var idea = s.state.ideas[idx].copy()
    var id = idea.id.copy()
    if not (idea.stage == BUILD or idea.stage == TEND):
        raise user_error(
            id
            + " is in "
            + stage_name(idea.stage)
            + ". It ships from Build or Tend: vinci advance "
            + id
        )
    var first = len(idea.ships) == 0
    var since = idea.born.utc
    if not first:
        since = idea.ships[len(idea.ships) - 1].at.utc
    var cycle = s.now.utc - since
    s.record(
        Event(
            s.now.copy(),
            "ship",
            id=id.copy(),
            text=what.copy(),
        )
    )
    var since_word = String("inception") if first else String("the last ship")
    print(
        id
        + " shipped (#"
        + String(len(idea.ships) + 1)
        + "). Cycle time "
        + span(cycle)
        + " since "
        + since_word
        + "; touch time "
        + span(idea.touch)
        + " in total."
    )
    print("Goal: " + goal_status(s, idx) + ".")
    var open = List[String]()
    for h in s.state.predictions:
        if idea_is(h.idea_id, id) and h.right is None:
            open.append(h.id.copy())
    if len(open) > 0:
        print("Predictions to settle: " + join_items(open, ", "))
    if first:
        print(id + " is now in Tend. Use it yourself: vinci used " + id)
        remind(s, TEND)


def used(mut s: Session, idx: Int, how: Optional[String]) raises:
    """Used: record that the shipped thing was used, and how it went."""
    var idea = s.state.ideas[idx].copy()
    var id = idea.id.copy()
    if len(idea.ships) == 0:
        raise user_error(id + " has not shipped, so there is nothing to use")
    if idea.stage == RETIRED:
        raise user_error(id + " is retired")
    s.record(
        Event(
            s.now.copy(),
            "used",
            id=id.copy(),
            text=how.copy(),
        )
    )
    print(
        id
        + " used "
        + String(len(idea.uses) + 1)
        + ' times. Anything wrong with it? vinci issue "..." '
        + id
    )


def tend(mut s: Session, idx: Int, what: String) raises:
    """Tend: record upkeep on an idea."""
    var idea = s.state.ideas[idx].copy()
    var id = idea.id.copy()
    if idea.stage == RETIRED:
        raise user_error(id + " is retired")
    s.record(
        Event(
            s.now.copy(),
            "tend",
            id=id.copy(),
            text=what.copy(),
        )
    )
    print(id + " tended.")


def retire(mut s: Session, idx: Int, why: String) raises:
    """Retire: end an idea on purpose. Its notes stay."""
    var idea = s.state.ideas[idx].copy()
    var id = idea.id.copy()
    if idea.stage == RETIRED:
        raise user_error(id + " is already retired")
    s.record(
        Event(
            s.now.copy(),
            "retire",
            id=id.copy(),
            text=why.copy(),
        )
    )
    print(id + " retired on purpose. Its notes stay.")


def ideas_list(s: Session) raises:
    """Ideas: every idea, with its stage, touch time, age and flow."""
    if len(s.state.ideas) == 0:
        print('No ideas yet. vinci idea "title"')
        return
    for idx in range(len(s.state.ideas)):
        var i = s.state.ideas[idx].copy()
        var age = s.now.utc - i.born.utc
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


def pipeline_detail(s: Session, idx: Int, stage: Int) raises -> String:
    """The text in brackets on one pipeline line, which depends on the stage."""
    var i = s.state.ideas[idx].copy()
    if stage == INCUBATE:
        if i.incubate_until is None:
            raise Error("journal: incubating idea has no date")
        return "until " + day(i.incubate_until.value())
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
            + String(len(i.ships))
            + ", used "
            + String(len(i.uses))
            + "; goal "
            + goal_status(s, idx)
        )
    return span(i.time_in(stage, s.now)) + " here"


def pipeline(s: Session) raises:
    """Pipeline: every live idea by stage, where ideas get stuck, and the one thing
    to do now."""
    var stages = List[Int]()
    stages.append(SEED)
    stages.append(PREPARE)
    stages.append(INCUBATE)
    stages.append(BUILD)
    stages.append(TEND)
    for st in stages:
        var here = List[Int]()
        for idx in range(len(s.state.ideas)):
            if s.state.ideas[idx].stage == st:
                here.append(idx)
        var label = stage_name(st)
        if st == BUILD:
            label = "Build " + String(len(here)) + "/" + String(BUILD_LIMIT)
        if len(here) == 0:
            print(pad_right(label, 10) + " -")
        for j in range(len(here)):
            var idx = here[j]
            var shown = String("")
            if j == 0:
                shown = label.copy()
            var i = s.state.ideas[idx].copy()
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

    var slow = slowest_stage(s.state, s.now)
    if slow is not None:
        var slowest = slow.value().copy()
        if slowest.average >= TICKS_PER_HOUR:
            print(
                "Slowest stage: "
                + stage_name(slowest.stage)
                + ", "
                + span(slowest.average)
                + " on average. Speed that one up first."
            )
    next_action(s)


def next_action(s: Session) raises:
    """Next: the one thing to do now, from review.mojo."""
    var step = next_step(s.state, s.now)
    print("Next: " + step.what)
    print("      " + step.how)


def adopt(mut s: Session, folder: String) raises:
    """Adopt: an existing folder becomes an idea, named after it. Adopting it
    again does nothing, so a whole directory can be adopted in a loop."""
    var path = folder_arg(folder)
    var owner = home_owner(s, path)
    if owner >= 0:
        print(s.state.ideas[owner].id + " already lives in " + path + ".")
        return
    var id = s.next_id("i")
    s.record(
        Event(
            s.now.copy(),
            "idea",
            id=id.copy(),
            text=file_name(path),
        )
    )
    s.record(
        Event(
            s.now.copy(),
            "home",
            id=id.copy(),
            text=path.copy(),
        )
    )
    print(
        id
        + " adopted from "
        + path
        + ". Its windows and commits now count as work."
    )


def home(mut s: Session, idx: Int, folder: String) raises:
    """Home: where the idea lives. Its editor windows and commits count as work.
    """
    var idea = s.state.ideas[idx].copy()
    var id = idea.id.copy()
    if idea.stage == RETIRED:
        raise user_error(id + " is retired")
    var path = folder_arg(folder)
    var owner = home_owner(s, path)
    if owner >= 0:
        raise user_error(s.state.ideas[owner].id + " already lives in " + path)
    s.record(
        Event(
            s.now.copy(),
            "home",
            id=id.copy(),
            text=path.copy(),
        )
    )
    print(
        id
        + " lives in "
        + path
        + '. Time in windows titled "'
        + home_pattern(path)
        + '" and commits there now count as work.'
    )


def watch(
    mut s: Session, idx: Int, pattern: String, kind: Optional[String]
) raises:
    """Watch: count time in windows whose app or title contains the text. With
    "use", such a window counts as using what was shipped."""
    var idea = s.state.ideas[idx].copy()
    var id = idea.id.copy()
    if idea.stage == RETIRED:
        raise user_error(id + " is retired")
    var is_use = False
    if kind is not None:
        if kind.value() != "use":
            raise user_error(
                'after the text comes "use" or nothing, got \''
                + kind.value()
                + "'"
            )
        is_use = True
    for w in s.state.all_watches():
        if equals_ignore_case(w.pattern, pattern):
            raise user_error(
                '"' + pattern + '" is already watched for ' + w.idea_id
            )
    var value: Optional[Float64] = None
    if is_use:
        value = Optional[Float64](1.0)
    s.record(
        Event(
            s.now.copy(),
            "watch",
            id=id.copy(),
            text=pattern.copy(),
            value=value^,
        )
    )
    if is_use:
        print(
            'A window whose app or title contains "'
            + pattern
            + '" now counts as using '
            + id
            + "."
        )
    else:
        print(
            'Time in a window whose app or title contains "'
            + pattern
            + '" now counts for '
            + id
            + "."
        )


def unwatch(mut s: Session, idx: Int, pattern: String) raises:
    """Unwatch: stop counting time for a pattern that was recorded for the idea.
    """
    var idea = s.state.ideas[idx].copy()
    var id = idea.id.copy()
    var found = False
    for w in s.state.watches:
        if w.idea_id == id and equals_ignore_case(w.pattern, pattern):
            found = True
    if not found:
        raise user_error(id + ' does not watch "' + pattern + '"')
    s.record(
        Event(
            s.now.copy(),
            "unwatch",
            id=id.copy(),
            text=pattern.copy(),
        )
    )
    print(id + ' no longer watches "' + pattern + '".')


def watches(s: Session) raises:
    """Watches: what is watched, recorded or implied by a home folder."""
    var all = s.state.all_watches()
    if len(all) == 0:
        print(NOTHING_WATCHED)
        return
    for w in all:
        var title = s.state.ideas[s.state.idea_index(w.idea_id)].title
        var mode = String("use ") if w.use else String("work")
        print(
            pad_right(w.idea_id, 4)
            + " "
            + mode
            + ' "'
            + w.pattern
            + '"  '
            + title
        )


def spent(mut s: Session, idx: Int, minutes: Float64) raises:
    """Spent: add time spent away from the screen."""
    if minutes <= 0.0:
        raise user_error("minutes must be positive")
    if minutes * Float64(TICKS_PER_MINUTE) >= TICKS_OVERFLOW_F:
        raise user_error("minutes is past the longest span vinci keeps")
    var idea = s.state.ideas[idx].copy()
    var id = idea.id.copy()
    var ticks = Int(minutes * Float64(TICKS_PER_MINUTE))
    if ticks > TICKS_MAX - idea.touch:
        raise user_error(id + " would pass the longest span vinci keeps")
    s.record(
        Event(
            s.now.copy(),
            "spent",
            id=id.copy(),
            value=minutes,
        )
    )
    print(span(ticks) + " added to " + id + ".")


def show_idea(s: Session, idea_index: Int) raises:
    """ShowIdea: the idea's full view, with its requirements, predictions, notes,
    credits, ships and issues."""
    var i = s.state.ideas[idea_index].copy()
    var now = s.now.copy()
    print(i.id + "  " + i.title)

    var stage_line = "stage   " + stage_name(i.stage)
    if i.stage == INCUBATE and i.incubate_until is not None:
        stage_line += " until " + day(i.incubate_until.value())
    if i.retired_why is not None:
        stage_line += ": " + i.retired_why.value()
    print(stage_line)

    var for_text = String("(nobody yet)")
    if i.for_ is not None:
        for_text = i.for_.value()
    print("for     " + for_text)

    var goal_line = String("goal    ")
    if i.goal is None:
        goal_line += "(none)"
    else:
        goal_line += i.goal.value() + "  [" + goal_status(s, idea_index) + "]"
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
        var home_line = "home    " + i.home.value()
        var n = len(i.work)
        if n > 0:
            var plural = String("") if n == 1 else String("s")
            home_line += (
                "  ("
                + String(n)
                + " commit"
                + plural
                + ", last "
                + day(i.work[n - 1].at)
                + ")"
            )
        print(home_line)

    var watch_items = List[String]()
    for w in s.state.all_watches():
        if w.idea_id == i.id:
            var item = '"' + w.pattern + '"'
            if w.use:
                item += " (use)"
            watch_items.append(item)
    if len(watch_items) > 0:
        print("watch   " + join_items(watch_items, ", "))

    var stage_parts = List[String]()
    var order = List[Int]()
    order.append(SEED)
    order.append(PREPARE)
    order.append(INCUBATE)
    order.append(BUILD)
    order.append(TEND)
    for st in order:
        if i.entered(st):
            var part = stage_name(st) + " " + span(i.time_in(st, now))
            if i.touch_in[st] != 0:
                part += " (touch " + span(i.touch_in[st]) + ")"
            stage_parts.append(part)
    print("stages  " + join_items(stage_parts, ", "))

    if len(i.ships) > 0:
        var uses = len(i.uses)
        var used_line = "used    " + String(uses) + " times"
        if uses > 0:
            used_line += ", last " + day(i.uses[uses - 1])
        print(used_line)

    var req_lines = List[String]()
    for r in s.state.requirements:
        if r.idea_id == i.id:
            if r.live():
                req_lines.append(
                    pad_right(r.id, 4)
                    + " "
                    + r.text
                    + "  ("
                    + r.who
                    + ": "
                    + r.why
                    + ")"
                )
            else:
                req_lines.append(
                    pad_right(r.id, 4)
                    + " CUT "
                    + r.text
                    + "  ("
                    + r.cut_why.copy().or_else("")
                    + ")"
                )
    s.section("requirements", req_lines)

    var pred_lines = List[String]()
    for h in s.state.predictions:
        if idea_is(h.idea_id, i.id):
            pred_lines.append(prediction_line(h))
    s.section("predictions", pred_lines)

    var about = s.state.notes_about(i.id)
    var note_lines = List[String]()
    for n in about:
        note_lines.append(note_line(s.state.notes[n]))
    s.section("notes", note_lines)

    var credit_lines = List[String]()
    var seen = List[String]()
    for n in about:
        for r in s.state.notes[n].refs:
            if r.startswith("s") and not has_string(seen, r):
                seen.append(r.copy())
                var k = s.state.source_index(r)
                if k < 0:
                    raise Error("journal: credit names a missing source: " + r)
                credit_lines.append(
                    pad_right(s.state.sources[k].id, 4)
                    + " "
                    + s.state.sources[k].citation
                )
    s.section("credits", credit_lines)

    var ship_lines = List[String]()
    for sh in i.ships:
        ship_lines.append(day(sh.at) + "  " + sh.what)
    s.section("ships", ship_lines)

    var issue_lines = List[String]()
    for x in s.state.issues:
        if idea_is(x.idea_id, i.id):
            issue_lines.append(issue_line(x))
    s.section("issues", issue_lines)
