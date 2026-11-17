# The ops group: help, path, and the improvement commands (issue, diagnose, principle,
# issues, principles, commit, kept, missed, review). Owns these `case` labels of Dispatch:
# issue, diagnose, principle, issues, principles, commit, kept, missed, review, path,
# help, --help, -h, sync.
#
# sync is not in the Mojo port: it shows what the C# tracker just counted from window and
# git activity, and this port never tracks, so it says so rather than print nothing.
#
# The unknown-command branch is not here: C# Dispatch ends with it, so vinci.mojo does too.
#
# `args` is the list after the command name (C#'s `a`). Return True when the command
# was handled, False to let the next group try it.

from journal import Event
from review import COMMITMENT_LENGTH, findings
from session import Session, user_error
from state import FINDINGS_METRIC, Issue, stage_name
from text import day, num, pad_right

# Dispatch's Help text (App.cs), byte for byte. `vinci help` prints it with no extra newline.
comptime HELP = """vinci: one journal for ideas, knowledge, time and measurements.
Run it with no command, or as "vinci chat", to talk to the chat model, which does
everything below through its tools. "vinci pipeline" shows the stages and the one
thing to do next. Everything below also works as a plain command.

An idea grows through five stages, and each move has a gate:
  Seed -> Prepare      needs who it is for, and a goal tied to a number
  Prepare -> Incubate  needs a note, a questioned requirement, a prediction
  Incubate -> Build    needs the wait to be over, and fewer than 3 in Build
  Build -> Tend        happens when you ship
  Tend                 never ends: ship again, use it, tend it, or retire it

Ideas
  idea "title"                        capture an idea
  for <idea> "who"                    who will use it
  goal <idea> "goal" <metric> at-least <n> | at-most <n> | between <a> <b>
  req <idea> "text" "who" "why"       a requirement, who asked, the reason
  cut <req> "why"                     delete a requirement
  advance <idea> [days]               pass the next gate, or say why not (days: incubation, default 1)
  ship <idea> "what"                  record something shipped
  used <idea> ["how it went"]         record that you used what you shipped
  tend <idea> "what"                  record upkeep on a grown idea
  retire <idea> "why"                 end an idea on purpose
  pipeline   next   ideas   show <id>

Time
  adopt <folder>                      make an existing folder an idea, named after it
  home <idea> <folder>                where the idea lives: its editor windows and commits count as work
  watch <idea> "text" [use]           count time in windows whose app or title contains the text;
                                      with "use", such a window counts as using what you shipped
  unwatch <idea> "text"               stop counting it
  watches                             what is watched
  sync                                show what was just counted (every command counts it)
  spent <idea> <minutes>              add time spent away from the screen

Portal
  bookmark "title" <url> ["what it is for"] [+tag...]
  unbookmark <b>   bookmarks
  serve [port]                        the page of saved links, on localhost (default port 4000).
                                      It also shows each project's portal.json and any web page
                                      this machine is serving.

Knowledge
  source "citation" [url]             where knowledge came from
  note "text" [id...] [+tag...]       one thought. s1:p.42 cites a place in a source
  link <note> <id>                    connect a note to a note, idea or prediction
  notes [word | +tag]   tags   sources

Inquiry
  predict "claim" <percent> [idea]    what you expect, before you test it
  result <h> right|wrong "what happened"
  predictions                         your record against your confidence

Measurement
  log <metric> <value> ["comment"]    measure anything
  spec <metric> <low> <high>          what counts as good
  stats [metric]                      control limits, and defects against a spec

Improvement
  issue "what went wrong" [idea] [big] [principle that failed]
  diagnose <issue> "root cause"       why it really happened
  principle "rule" [issue]            what you do from now on
  issues   principles
  commit "one thing" [idea]           what you will get done this week
  kept | missed "why"                 answer for it
  review                              what needs fixing now. Run it weekly.

In a pipe
  Any quoted text can be "-" to read it from standard input:
    git log -1 --format=%s | vinci ship i1 -
  vinci path prints the journal file, one JSON object per line:
    jq -r 'select(.kind=="ship") | .text' "$(vinci path)"

Data lives in $VINCI_HOME/journal.jsonl (default ~/.vinci).
Window time comes from ActivityWatch at $VINCI_ACTIVITYWATCH (default http://127.0.0.1:5600).
"""


def tail(args: List[String], start: Int) -> List[String]:
    """The arguments from start on, as a new list (C#'s a[start..])."""
    var out = List[String]()
    for i in range(start, len(args)):
        out.append(args[i])
    return out^


def issue_line(x: Issue) raises -> String:
    """IssueLine: id, closed, diagnosed or open, BIG when big, then the text."""
    var status: String
    if x.principle_id is not None:
        status = "closed   "
    elif x.root_cause is not None:
        status = "diagnosed"
    else:
        status = "open     "
    var big = "BIG " if x.big else ""
    return pad_right(x.id, 4) + " " + status + " " + big + x.text


def commitment_record(s: Session, kept: Int, missed: Int) raises -> String:
    """CommitmentRecord: how many commitments were kept, of all answered for."""
    var kept_total = kept
    var not_kept = 0
    for c in s.state.commitments:
        if c.kept is not None:
            if c.kept.value():
                kept_total += 1
            else:
                not_kept += 1
    var total = kept_total + missed + not_kept
    return String(kept_total) + " of " + String(total) + " commitments kept."


def new_issue(mut s: Session, text: String, rest: List[String]) raises:
    """NewIssue: the idea or principle it happened on, and the word big, in any order.
    """
    var refs = List[String]()
    var big = False
    for token in rest:
        if token == "big":
            big = True
        elif (
            s.state.idea_index(token) >= 0
            or s.state.principle_index(token) >= 0
        ):
            refs.append(token)
        else:
            raise user_error(
                "'" + token + "' is not an idea, a principle, or the word big"
            )
    var id = s.next_id("x")
    var refs_arg: Optional[List[String]] = None
    if len(refs) > 0:
        refs_arg = refs^
    var value: Optional[Float64] = None
    if big:
        value = 1.0
    s.record(
        Event(
            s.now.copy(),
            "issue",
            id=id,
            text=text,
            refs=refs_arg^,
            value=value,
        )
    )
    print(
        id
        + " logged. Do not solve it yet. Find the root cause: vinci diagnose "
        + id
        + ' "..."'
    )


def diagnose(mut s: Session, id: String, root_cause: String) raises:
    """Diagnose: the root cause of an issue."""
    var i = s.state.issue_index(id)
    if i < 0:
        raise user_error("no issue " + id)
    var issue_id = s.state.issues[i].id.copy()
    s.record(Event(s.now.copy(), "diagnose", id=issue_id, text=root_cause))
    print(
        issue_id
        + ' diagnosed. Now change the machine: vinci principle "what I do from'
        ' now on" '
        + issue_id
    )


def new_principle(
    mut s: Session, text: String, issue_arg: Optional[String]
) raises:
    """NewPrinciple: a rule, optionally closing a diagnosed issue."""
    if issue_arg is not None:
        var issue_id = issue_arg.value()
        var x = s.state.issue_index(issue_id)
        if x < 0:
            raise user_error("no issue " + issue_id)
        if s.state.issues[x].root_cause is None:
            raise user_error(
                issue_id
                + " has no root cause. Diagnose before you design: vinci"
                " diagnose "
                + issue_id
                + ' "..."'
            )
        if s.state.issues[x].principle_id is not None:
            raise user_error(
                issue_id
                + " is already closed by "
                + s.state.issues[x].principle_id.value()
            )
    var id = s.next_id("p")
    var refs_arg: Optional[List[String]] = None
    if issue_arg is not None:
        var one = List[String]()
        one.append(issue_arg.value())
        refs_arg = one^
    s.record(Event(s.now.copy(), "principle", id=id, text=text, refs=refs_arg^))
    var closed = ""
    if issue_arg is not None:
        closed = " " + issue_arg.value() + " is closed."
    print(id + " adopted." + closed + ' If it fails: vinci issue "..." ' + id)


def issues(s: Session) raises:
    """Issues: every issue, then where they usually go wrong."""
    if len(s.state.issues) == 0:
        print('No issues logged. vinci issue "what went wrong"')
        return
    for x in s.state.issues:
        print(issue_line(x))

    # Group by stage in first-appearance order, then a stable sort, most first.
    var stages = List[Int]()
    var counts = List[Int]()
    for x in s.state.issues:
        if x.stage is None:
            continue
        var stage = x.stage.value()
        var found = -1
        for k in range(len(stages)):
            if stages[k] == stage:
                found = k
        if found < 0:
            stages.append(stage)
            counts.append(1)
        else:
            counts[found] += 1
    for a in range(1, len(stages)):
        var j = a
        while j > 0 and counts[j - 1] < counts[j]:
            var tmp_count = counts[j]
            counts[j] = counts[j - 1]
            counts[j - 1] = tmp_count
            var tmp_stage = stages[j]
            stages[j] = stages[j - 1]
            stages[j - 1] = tmp_stage
            j -= 1
    if len(stages) > 0:
        var parts = List[String]()
        for k in range(len(stages)):
            parts.append(stage_name(stages[k]) + " " + String(counts[k]))
        var line = "By stage: "
        for k in range(len(parts)):
            if k > 0:
                line += ", "
            line += parts[k]
        print(line + ". Most go wrong in " + stage_name(stages[0]) + ".")


def principles(s: Session) raises:
    """Principles: each rule, the issue it came from, and how often it broke."""
    if len(s.state.principles) == 0:
        print("No principles yet. They come from diagnosed issues.")
        return
    for p in s.state.principles:
        var from_part = ""
        if p.issue_id is not None:
            var stage_part = ""
            if p.stage is not None:
                stage_part = ", " + stage_name(p.stage.value())
            from_part = "  (from " + p.issue_id.value() + stage_part + ")"
        var n = s.state.breaks(p.id)
        var breaks = ""
        if n > 0:
            breaks = "  broken " + String(n) + " times"
        print(pad_right(p.id, 4) + " " + p.text + from_part + breaks)


def commit(mut s: Session, text: String, idea: Optional[String]) raises:
    """Commit: what you will get done this week, optionally on an idea."""
    var open_index = s.state.open_commitment_index()
    if open_index >= 0:
        raise user_error(
            'you already committed to "'
            + s.state.commitments[open_index].text
            + '". Answer for it first: vinci kept, or vinci missed "why"'
        )
    if idea is not None and s.state.idea_index(idea.value()) < 0:
        raise user_error("no idea " + idea.value())
    var refs_arg: Optional[List[String]] = None
    if idea is not None:
        var one = List[String]()
        one.append(idea.value())
        refs_arg = one^
    s.record(Event(s.now.copy(), "commit", text=text, refs=refs_arg^))
    print("Committed. Due " + day(s.now.add_ticks(COMMITMENT_LENGTH)) + ".")


def kept(mut s: Session) raises:
    """Kept: the open commitment was done."""
    if s.state.open_commitment_index() < 0:
        raise user_error("there is no open commitment")
    s.record(Event(s.now.copy(), "kept"))
    print(
        "Kept. "
        + commitment_record(s, 1, 0)
        + ' What is next? vinci commit "..."'
    )


def missed(mut s: Session, why: String) raises:
    """Missed: the open commitment was not done. That is logged as an issue."""
    var open_index = s.state.open_commitment_index()
    if open_index < 0:
        raise user_error("there is no open commitment")
    var open_text = s.state.commitments[open_index].text
    var open_idea = s.state.commitments[open_index].idea_id
    s.record(Event(s.now.copy(), "missed", text=why))
    var id = s.next_id("x")
    var refs_arg: Optional[List[String]] = None
    if open_idea is not None:
        var one = List[String]()
        one.append(open_idea.value())
        refs_arg = one^
    s.record(
        Event(
            s.now.copy(),
            "issue",
            id=id,
            text='Missed commitment "' + open_text + '": ' + why,
            refs=refs_arg^,
        )
    )
    print(
        "Missed. "
        + commitment_record(s, 0, 1)
        + " Logged as "
        + id
        + ": vinci diagnose "
        + id
        + ' "root cause"'
    )


def run_review(mut s: Session) raises:
    """RunReview: what needs fixing now, the trend, and a review event for the record.
    """
    var found = findings(s.state, s.now)
    for f in found:
        print(pad_right(f.subject, 6) + " " + f.problem)
        print("       fix: " + f.fix)

    var trend = ""
    var m = s.state.metric_index(FINDINGS_METRIC)
    if m >= 0 and len(s.state.metrics[m].points) > 0:
        var last = (
            s.state.metrics[m].points[len(s.state.metrics[m].points) - 1].value
        )
        trend = " Last review: " + num(last) + "."
    var count = len(found)
    if count == 0:
        print("Nothing to fix." + trend)
    else:
        print(String(count) + " to fix." + trend)

    var answered = False
    for c in s.state.commitments:
        if c.kept is not None:
            answered = True
    if answered:
        print(commitment_record(s, 0, 0))
    s.record(Event(s.now.copy(), "review", value=Float64(count)))


def dispatch_ops(
    mut s: Session, cmd: String, args: List[String]
) raises -> Bool:
    if cmd == "help" or cmd == "--help" or cmd == "-h":
        print(HELP, end="")
        return True
    if cmd == "path":
        print(s.journal.path)
        return True
    if cmd == "sync":
        # The oracle with VINCI_NO_TRACK=1: nothing is tracked, so nothing is
        # printed and nothing is appended. The journal was read by Session.
        return True
    if cmd == "issue":
        var text = s.arg(args, 0, "what went wrong")
        new_issue(s, text, tail(args, 1))
        return True
    if cmd == "diagnose":
        var id = s.arg(args, 0, "issue id")
        var cause = s.arg(args, 1, "root cause")
        diagnose(s, id, cause)
        return True
    if cmd == "principle":
        var text = s.arg(args, 0, "principle")
        var issue_arg: Optional[String] = None
        if len(args) > 1:
            issue_arg = args[1]
        new_principle(s, text, issue_arg)
        return True
    if cmd == "issues":
        issues(s)
        return True
    if cmd == "principles":
        principles(s)
        return True
    if cmd == "commit":
        var text = s.arg(args, 0, "what you will get done")
        var idea_arg: Optional[String] = None
        if len(args) > 1:
            idea_arg = args[1]
        commit(s, text, idea_arg)
        return True
    if cmd == "kept":
        kept(s)
        return True
    if cmd == "missed":
        var why = s.arg(args, 0, "why")
        missed(s, why)
        return True
    if cmd == "review":
        run_review(s)
        return True
    return False
