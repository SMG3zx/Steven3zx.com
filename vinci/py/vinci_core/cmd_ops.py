"""The ops group: help, path, and the improvement commands (issue, diagnose, principle,
issues, principles, commit, kept, missed, review). The tracker runs before every command, so
there is no sync command: its lines are written to the journal and the chat reports them.

Return True when the command was handled, False to let the next group try it.

Port of mojo/src/cmd_ops.mojo.
"""

from __future__ import annotations

from typing import Optional

from .journal import Event
from .review import COMMITMENT_LENGTH, findings
from .session import Session, user_error
from .state import FINDINGS_METRIC, stage_name
from .cmd_ideas import issue_line
from .brief import brief
from .text import day, num, pad_right, tail

HELP = """vinci: one journal for ideas, knowledge, time and measurements.

Talk to it with no command, or as "vinci chat". The chat model keeps your ideas, notes,
time and goals in the journal and does the work through its tools.

  vinci                    the chat
  vinci chat              the chat (the same)
  vinci serve [port]       the page of saved links, on localhost (default port 4000)
  vinci index [...]        build or search the index of the journal
  vinci feed [...]         read the feeds into the index
  vinci backup [...]       copy the journal to a backup folder
  vinci verify [...]       check the journal against its backups
  vinci brief              the daily brief: commitments, review, this week, time per idea
  vinci path               print where the journal is
  vinci help               this text
"""


def commitment_record(s: Session, kept: int, missed: int) -> str:
    """CommitmentRecord: how many commitments were kept, of all answered for."""
    kept_total = kept
    not_kept = 0
    for c in s.state.commitments:
        if c.kept is not None:
            if c.kept:
                kept_total += 1
            else:
                not_kept += 1
    total = kept_total + missed + not_kept
    return str(kept_total) + " of " + str(total) + " commitments kept."


def new_issue(s: Session, text: str, rest: list[str]) -> None:
    """NewIssue: the idea or principle it happened on, and the word big, in any order."""
    refs: list[str] = []
    big = False
    for token in rest:
        if token == "big":
            big = True
        elif s.state.idea_index(token) >= 0 or s.state.principle_index(token) >= 0:
            refs.append(token)
        else:
            raise user_error("'" + token + "' is not an idea, a principle, or the word big")
    ident = s.next_id("x")
    value: Optional[float] = 1.0 if big else None
    s.record(
        Event(
            s.now,
            "issue",
            id=ident,
            text=text,
            refs=refs if len(refs) > 0 else None,
            value=value,
        )
    )
    print(ident + " logged. Do not solve it yet. Find the root cause: vinci journal diagnose " + ident + ' "..."')


def diagnose(s: Session, ident: str, root_cause: str) -> None:
    """Diagnose: the root cause of an issue."""
    i = s.state.issue_index(ident)
    if i < 0:
        raise user_error("no issue " + ident)
    issue_id = s.state.issues[i].id
    s.record(Event(s.now, "diagnose", id=issue_id, text=root_cause))
    print(
        issue_id
        + ' diagnosed. Now change the machine: vinci journal principle "what I do from now on" '
        + issue_id
    )


def new_principle(s: Session, text: str, issue_arg: Optional[str]) -> None:
    """NewPrinciple: a rule, optionally closing a diagnosed issue."""
    if issue_arg is not None:
        issue_id = issue_arg
        x = s.state.issue_index(issue_id)
        if x < 0:
            raise user_error("no issue " + issue_id)
        if s.state.issues[x].root_cause is None:
            raise user_error(
                issue_id + " has no root cause. Diagnose before you design: vinci journal diagnose " + issue_id + ' "..."'
            )
        if s.state.issues[x].principle_id is not None:
            raise user_error(issue_id + " is already closed by " + str(s.state.issues[x].principle_id))
    ident = s.next_id("p")
    refs = [issue_arg] if issue_arg is not None else None
    s.record(Event(s.now, "principle", id=ident, text=text, refs=refs))
    closed = ""
    if issue_arg is not None:
        closed = " " + issue_arg + " is closed."
    print(ident + " adopted." + closed + ' If it fails: vinci journal issue "..." ' + ident)


def issues(s: Session) -> None:
    """Issues: every issue, then where they usually go wrong."""
    if len(s.state.issues) == 0:
        print('No issues logged. vinci journal issue "what went wrong"')
        return
    for x in s.state.issues:
        print(issue_line(x))

    # Group by stage in first-appearance order, then a stable sort, most first.
    stages: list[int] = []
    counts: list[int] = []
    for x in s.state.issues:
        if x.stage is None:
            continue
        stage = x.stage
        found = -1
        for k in range(len(stages)):
            if stages[k] == stage:
                found = k
        if found < 0:
            stages.append(stage)
            counts.append(1)
        else:
            counts[found] += 1
    for a in range(1, len(stages)):
        j = a
        while j > 0 and counts[j - 1] < counts[j]:
            counts[j], counts[j - 1] = counts[j - 1], counts[j]
            stages[j], stages[j - 1] = stages[j - 1], stages[j]
            j -= 1
    if len(stages) > 0:
        parts = [stage_name(stages[k]) + " " + str(counts[k]) for k in range(len(stages))]
        line = "By stage: " + ", ".join(parts)
        print(line + ". Most go wrong in " + stage_name(stages[0]) + ".")


def principles(s: Session) -> None:
    """Principles: each rule, the issue it came from, and how often it broke."""
    if len(s.state.principles) == 0:
        print("No principles yet. They come from diagnosed issues.")
        return
    for p in s.state.principles:
        from_part = ""
        if p.issue_id is not None:
            stage_part = ""
            if p.stage is not None:
                stage_part = ", " + stage_name(p.stage)
            from_part = "  (from " + p.issue_id + stage_part + ")"
        n = s.state.breaks(p.id)
        breaks = ""
        if n > 0:
            breaks = "  broken " + str(n) + " times"
        print(pad_right(p.id, 4) + " " + p.text + from_part + breaks)


def commit(s: Session, text: str, idea: Optional[str]) -> None:
    """Commit: what you will get done this week, optionally on an idea."""
    open_index = s.state.open_commitment_index()
    if open_index >= 0:
        raise user_error(
            'you already committed to "'
            + s.state.commitments[open_index].text
            + '". Answer for it first: vinci journal kept, or vinci journal missed "why"'
        )
    if idea is not None and s.state.idea_index(idea) < 0:
        raise user_error("no idea " + idea)
    refs = [idea] if idea is not None else None
    s.record(Event(s.now, "commit", text=text, refs=refs))
    print("Committed. Due " + day(s.now.add_ticks(COMMITMENT_LENGTH)) + ".")


def kept(s: Session) -> None:
    """Kept: the open commitment was done."""
    if s.state.open_commitment_index() < 0:
        raise user_error("there is no open commitment")
    s.record(Event(s.now, "kept"))
    print("Kept. " + commitment_record(s, 1, 0) + ' What is next? vinci journal commit "..."')


def missed(s: Session, why: str) -> None:
    """Missed: the open commitment was not done. That is logged as an issue."""
    open_index = s.state.open_commitment_index()
    if open_index < 0:
        raise user_error("there is no open commitment")
    open_text = s.state.commitments[open_index].text
    open_idea = s.state.commitments[open_index].idea_id
    s.record(Event(s.now, "missed", text=why))
    ident = s.next_id("x")
    refs = [open_idea] if open_idea is not None else None
    s.record(
        Event(
            s.now,
            "issue",
            id=ident,
            text='Missed commitment "' + open_text + '": ' + why,
            refs=refs,
        )
    )
    print(
        "Missed. "
        + commitment_record(s, 0, 1)
        + " Logged as "
        + ident
        + ": vinci journal diagnose "
        + ident
        + ' "root cause"'
    )


def run_review(s: Session) -> None:
    """RunReview: what needs fixing now, the trend, and a review event for the record."""
    found = findings(s.state, s.now)
    for f in found:
        print(pad_right(f.subject, 6) + " " + f.problem)
        print("       fix: " + f.fix)

    trend = ""
    m = s.state.metric_index(FINDINGS_METRIC)
    if m >= 0 and len(s.state.metrics[m].points) > 0:
        last = s.state.metrics[m].points[-1].value
        trend = " Last review: " + num(last) + "."
    count = len(found)
    if count == 0:
        print("Nothing to fix." + trend)
    else:
        print(str(count) + " to fix." + trend)

    answered = any(c.kept is not None for c in s.state.commitments)
    if answered:
        print(commitment_record(s, 0, 0))
    s.record(Event(s.now, "review", value=float(count)))


def dispatch_ops(s: Session, cmd: str, args: list[str]) -> bool:
    if cmd == "help":
        print(HELP, end="")
        return True
    if cmd == "brief":
        print(brief(s.journal.read(), s.state, s.now), end="")
        return True
    if cmd == "path":
        print(s.journal.path)
        return True
    if cmd == "issue":
        text = s.arg(args, 0, "what went wrong")
        new_issue(s, text, tail(args, 1))
        return True
    if cmd == "diagnose":
        ident = s.arg(args, 0, "issue id")
        cause = s.arg(args, 1, "root cause")
        diagnose(s, ident, cause)
        return True
    if cmd == "principle":
        text = s.arg(args, 0, "principle")
        issue_arg = args[1] if len(args) > 1 else None
        new_principle(s, text, issue_arg)
        return True
    if cmd == "issues":
        issues(s)
        return True
    if cmd == "principles":
        principles(s)
        return True
    if cmd == "commit":
        text = s.arg(args, 0, "what you will get done")
        idea_arg = args[1] if len(args) > 1 else None
        commit(s, text, idea_arg)
        return True
    if cmd == "kept":
        kept(s)
        return True
    if cmd == "missed":
        why = s.arg(args, 0, "why")
        missed(s, why)
        return True
    if cmd == "review":
        run_review(s)
        return True
    return False
