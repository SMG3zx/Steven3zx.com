"""The knowledge, inquiry and measurement commands.

Owns: bookmark, unbookmark, bookmarks, source, note, link, notes, tags, sources, show,
predict, result, predictions, log, spec, stats. show is the dispatcher for every id: an idea
id goes to show_idea in cmd_ideas, and the other ids (note, source, issue, prediction) are
written here.

Known gap: case folding of tags and search terms is ASCII-only, so a non-ASCII tag or search
term is not folded.

Port of mojo/src/cmd_knowledge.mojo.
"""

from __future__ import annotations

from typing import Optional

from .cmd_ideas import show_idea
from .journal import Event
from .jsonline import ascii_lower
from .session import Session, note_line, number, prediction_line, user_error
from .state import FINDINGS_METRIC, RETIRED, Note, stage_name
from .stats import control_limits, mean
from .text import day, num, pad_right
from .timeutil import Instant


def tail(a: list[str], start: int) -> list[str]:
    """The items from start on: empty when start is past the end."""
    return list(a[start:])


def is_web_address(url: str) -> bool:
    """An absolute http or https address with something after the scheme."""
    lower = ascii_lower(url)
    if lower.startswith("http://"):
        rest = url[7:]
    elif lower.startswith("https://"):
        rest = url[8:]
    else:
        return False
    return len(rest) > 0


def contains_ignore_case(hay: str, needle: str) -> bool:
    """String.Contains with OrdinalIgnoreCase, for ASCII. An empty needle matches."""
    return ascii_lower(needle) in ascii_lower(hay)


def linkable(s: Session, ident: str) -> bool:
    """A note, idea, source or prediction that exists. The first letter says which kind, so
    only n, i, s and h are tried."""
    if len(ident) == 0:
        return False
    if ident[0] not in ("n", "i", "s", "h"):
        return False
    return s.state.has(ident)


def note_lines_about(s: Session, ident: str) -> list[str]:
    """The note lines of every note that refers to ident, in journal order."""
    return [note_line(s.state.notes[i]) for i in s.state.notes_about(ident)]


def new_bookmark(s: Session, title: str, url: str, rest: list[str]) -> None:
    """A saved link. +words are tags, the rest is why it is kept."""
    if not is_web_address(url):
        raise user_error("'" + url + "' is not an http or https address")
    for b in s.state.bookmarks:
        if b.url == url:
            raise user_error(b.id + " already points there: " + b.title)
    tags: list[str] = []
    why_words: list[str] = []
    for r in rest:
        if r.startswith("+"):
            if len(r) > 1:
                tags.append(r[1:])
        else:
            why_words.append(r)
    why = " ".join(why_words)
    ident = s.state.next_id("b")
    s.record(
        Event(
            s.now,
            "bookmark",
            id=ident,
            text=title,
            url=url,
            why=why if len(why) > 0 else None,
            tags=tags if len(tags) > 0 else None,
        )
    )
    print(ident + " saved. It is on the portal: vinci serve")


def has_bookmark(s: Session, ident: str) -> bool:
    return any(b.id == ident for b in s.state.bookmarks)


def unbookmark(s: Session, ident: str) -> None:
    """Take a saved link off the portal."""
    if not has_bookmark(s, ident):
        raise user_error("no bookmark " + ident)
    s.record(Event(s.now, "unbookmark", id=ident))
    print(ident + " is off the portal.")


def bookmarks(s: Session) -> None:
    """Every saved link, one per line."""
    if len(s.state.bookmarks) == 0:
        print('No bookmarks yet. vinci bookmark "title" <url> ["what it is for"] [+tag...]')
        return
    for b in s.state.bookmarks:
        line = pad_right(b.id, 4) + " " + b.title + "  " + b.url
        for t in b.tags:
            line += " +" + t
        if b.why is not None:
            line += "  (" + b.why + ")"
        print(line)


def new_source(s: Session, citation: str, url: Optional[str]) -> None:
    """A place knowledge came from. Notes cite it by id."""
    ident = s.state.next_id("s")
    s.record(Event(s.now, "source", id=ident, text=citation, url=url))
    print(ident + ' recorded. Cite it with the place: vinci note "one idea, in your own' + ' words" ' + ident + ":p.12")


def colon_index(text: str) -> int:
    return text.find(":")


def new_note(s: Session, text: str, rest: list[str]) -> None:
    """After the text come, in any order, ids to link to, a source with an optional place
    (s1:p.42), and tags (+flight)."""
    refs: list[str] = []
    tags: list[str] = []
    where_: Optional[str] = None
    for token in rest:
        if token.startswith("+") and len(token) > 1:
            tags.append(ascii_lower(token[1:]))
            continue
        ident = token
        colon = colon_index(token)
        place: Optional[str] = None
        if colon >= 0:
            ident = token[:colon]
            place = token[colon + 1 :]
        if not linkable(s, ident):
            raise user_error(
                "'" + token + "' is not a note, idea, source or prediction that exists, nor a +tag"
            )
        if place is not None:
            if ident[0] != "s":
                raise user_error("only a source has a place inside it, and " + ident + " is not a source")
            where_ = place
        refs.append(ident)
    note_id = s.state.next_id("n")
    has_source = any(r[0] == "s" for r in refs)
    s.record(
        Event(
            s.now,
            "note",
            id=note_id,
            text=text,
            refs=refs if len(refs) > 0 else None,
            tags=tags if len(tags) > 0 else None,
            where_=where_,
        )
    )
    origin = ""
    if not has_source:
        origin = " No source given, so it is recorded as your own thought."
    print(note_id + " written." + origin)


def link(s: Session, from_id: str, to_id: str) -> None:
    """A note points at another note, idea, source or prediction."""
    n = s.state.note_index(from_id)
    if n < 0:
        raise user_error("no note " + from_id)
    if not linkable(s, to_id):
        raise user_error("'" + to_id + "' is not a note, idea, source or prediction that exists")
    if from_id == to_id:
        raise user_error("a note cannot link to itself")
    for r in s.state.notes[n].refs:
        if r == to_id:
            raise user_error(from_id + " already links to " + to_id)
    s.record(Event(s.now, "link", id=from_id, refs=[to_id]))
    print(from_id + " → " + to_id)


def notes(s: Session, query: Optional[str]) -> None:
    """Every note, or those matching a word in the text or a +tag."""
    found = 0
    for note in s.state.notes:
        keep = True
        if query is not None:
            q = query
            if q.startswith("+"):
                keep = False
                want = ascii_lower(q[1:])
                for t in note.tags:
                    if t == want:
                        keep = True
            else:
                keep = contains_ignore_case(note.text, q)
        if keep:
            found += 1
            print(note_line(note))
    if found == 0:
        print("No notes.")


def tags(s: Session) -> None:
    """The index of subjects, most used first, then by name."""
    counts: dict[str, int] = {}
    for note in s.state.notes:
        for t in note.tags:
            counts[t] = counts.get(t, 0) + 1
    if len(counts) == 0:
        print('No tags yet. Add one to a note: vinci note "..." +subject')
        return
    # Count descending, then name ordinal. Names are unique.
    for name, count in sorted(counts.items(), key=lambda kv: (-kv[1], kv[0])):
        print("+" + pad_right(name, 20) + " " + str(count))


def sources(s: Session) -> None:
    """Every source with its url and how many notes cite it."""
    if len(s.state.sources) == 0:
        print('No sources yet. vinci source "citation" [url]')
        return
    for src in s.state.sources:
        line = pad_right(src.id, 4) + " " + src.citation
        if src.url is not None:
            line += " <" + src.url + ">"
        line += "  (" + str(len(s.state.notes_about(src.id))) + " notes)"
        print(line)


def show_note(s: Session, n: int) -> None:
    """The full view of one note."""
    note: Note = s.state.notes[n]
    print(note.id + "  " + note.text)
    print("written  " + day(note.at))
    if len(note.tags) > 0:
        print("tags     " + " ".join("+" + t for t in note.tags))
    cited = False
    for r in note.refs:
        if r[0] == "s":
            cited = True
            si = s.state.source_index(r)
            if si < 0:
                raise ValueError("journal: note cites a source that does not exist: " + r)
            src = s.state.sources[si]
            line = "origin   " + src.id + " " + src.citation
            if note.where_ is not None:
                line += ", " + note.where_
            if src.url is not None:
                line += " <" + src.url + ">"
            print(line)
    if not cited:
        print("origin   your own thought")
    links = [s.describe(r) for r in note.refs if r[0] != "s"]
    s.section("links to", links)
    s.section("linked from", note_lines_about(s, note.id))


def show_issue(s: Session, x: int) -> None:
    """For an issue: what went wrong, its cause and its principle."""
    issue = s.state.issues[x]
    print(issue.id + "  " + issue.text + ("  (big)" if issue.big else ""))
    raised = "raised      " + day(issue.at)
    if issue.idea_id is not None:
        stage = stage_name(issue.stage) if issue.stage is not None else ""
        raised += " on " + issue.idea_id + " in " + stage
    print(raised)
    if issue.broke_id is not None:
        p = s.state.principle_index(issue.broke_id)
        if p < 0:
            raise ValueError("journal: issue names a principle that does not exist")
        print("broke       " + issue.broke_id + " " + s.state.principles[p].text)
    cause = "(not diagnosed)"
    if issue.root_cause is not None:
        cause = issue.root_cause
    print("root cause  " + cause)
    principle_line = "(none yet)"
    if issue.principle_id is not None:
        p = s.state.principle_index(issue.principle_id)
        if p >= 0:
            principle_line = s.state.principles[p].id + " " + s.state.principles[p].text
    print("principle   " + principle_line)


def show_prediction(s: Session, h: int) -> None:
    """For a prediction: the claim, when it was made, and the outcome."""
    pred = s.state.predictions[h]
    print(prediction_line(pred))
    made = "made     " + day(pred.at)
    if pred.idea_id is not None:
        made += " about " + pred.idea_id
    print(made)
    if pred.outcome is not None:
        print("outcome  " + pred.outcome)
    s.section("notes about it", note_lines_about(s, pred.id))


def show_source(s: Session, si: int) -> None:
    """For a source: its citation, url, date, and the notes drawn from it."""
    src = s.state.sources[si]
    print(src.id + "  " + src.citation)
    if src.url is not None:
        print("url       " + src.url)
    print("recorded  " + day(src.at))
    s.section("notes drawn from it", note_lines_about(s, src.id))


def show(s: Session, ident: str) -> None:
    """The view of any id. Ideas go to show_idea; the rest are written here."""
    i = s.state.idea_index(ident)
    if i >= 0:
        show_idea(s, i)
        return
    n = s.state.note_index(ident)
    if n >= 0:
        show_note(s, n)
        return
    si = s.state.source_index(ident)
    if si >= 0:
        show_source(s, si)
        return
    x = s.state.issue_index(ident)
    if x >= 0:
        show_issue(s, x)
        return
    h = s.state.prediction_index(ident)
    if h >= 0:
        show_prediction(s, h)
        return
    raise user_error("nothing has the id '" + ident + "'")


def predict(s: Session, text: str, percent: float, idea_id: Optional[str]) -> None:
    """Write down what you expect and how sure you are, before the test."""
    if percent < 1.0 or percent > 99.0:
        raise user_error("confidence is a percent from 1 to 99")
    if idea_id is not None and s.state.idea_index(idea_id) < 0:
        raise user_error("no idea " + idea_id)
    ident = s.state.next_id("h")
    refs = [idea_id] if idea_id is not None else None
    s.record(Event(s.now, "predict", id=ident, text=text, refs=refs, value=percent))
    print(
        ident
        + " predicted at "
        + num(percent)
        + '%. Test it, then: vinci result '
        + ident
        + ' right|wrong "what happened"'
    )


def result(s: Session, ident: str, verdict: str, outcome: str) -> None:
    """Settle a prediction, right or wrong, with what happened."""
    h = s.state.prediction_index(ident)
    if h < 0:
        raise user_error("no prediction " + ident)
    if s.state.predictions[h].right is not None:
        raise user_error(ident + " is already settled")
    if verdict != "right" and verdict != "wrong":
        raise user_error("say right or wrong, not '" + verdict + "'")
    value = 1.0 if verdict == "right" else 0.0
    s.record(Event(s.now, "result", id=ident, text=outcome, value=value))
    print(ident + " was " + verdict + '. Keep what you learned: vinci note "..." ' + ident)


def predictions(s: Session) -> None:
    """Every prediction, then your calibration once some are settled."""
    if len(s.state.predictions) == 0:
        print('No predictions yet. vinci predict "what you expect" <percent> [idea]')
        return
    for h in s.state.predictions:
        print(prediction_line(h))

    settled = 0
    right_count = 0
    confidence_sum = 0.0
    for h in s.state.predictions:
        if h.right is not None:
            settled += 1
            if h.right:
                right_count += 1
            confidence_sum += h.confidence
    if settled == 0:
        return
    right = 100.0 * float(right_count) / float(settled)
    sure = confidence_sum / float(settled)
    enough = 10
    if settled < enough:
        verdict = "Too few to judge (" + str(settled) + " of " + str(enough) + ")."
    elif abs(sure - right) < 10.0:
        verdict = "Your confidence matches your record."
    elif sure > right:
        verdict = "You are overconfident by " + num(sure - right) + " points."
    else:
        verdict = "You are underconfident by " + num(right - sure) + " points."
    print(
        "Settled "
        + str(settled)
        + ": right "
        + num(right)
        + "%, average confidence "
        + num(sure)
        + "%. "
        + verdict
    )


def log_value(s: Session, metric: str, value: float, comment: Optional[str]) -> None:
    """One measurement, and whether each goal on this metric is now met."""
    if metric == FINDINGS_METRIC:
        raise user_error(metric + " is kept by vinci itself")
    s.record(Event(s.now, "log", id=metric, text=comment, value=value))
    n = 1
    m = s.state.metric_index(metric)
    if m >= 0:
        n = len(s.state.metrics[m].points) + 1
    print(metric + " = " + num(value) + " (measurement " + str(n) + ").")
    for idea in s.state.ideas:
        if idea.goal_metric is None or idea.goal_metric != metric:
            continue
        if idea.stage == RETIRED:
            continue
        met = True
        if idea.goal_min is not None and not (value >= idea.goal_min):
            met = False
        if idea.goal_max is not None and not (value <= idea.goal_max):
            met = False
        verdict = "met" if met else "not met"
        print(idea.id + " goal " + verdict + ": needs " + idea.goal_target() + ".")


def spec(s: Session, metric: str, lo: float, hi: float) -> None:
    """Say what good looks like for a metric, between two limits."""
    if lo > hi:
        raise user_error("low must not be above high")
    s.record(Event(s.now, "spec", id=metric, value=lo, hi=hi))
    print(metric + " is good between " + num(lo) + " and " + num(hi) + ".")


def metrics(s: Session) -> None:
    """Stats with no name: one line per measured metric."""
    if len(s.state.metrics) == 0:
        print("Nothing measured yet. vinci log <metric> <value>")
        return
    for m in s.state.metrics:
        count = len(m.points)
        if count == 0:
            continue
        values = [p.value for p in m.points]
        last = m.points[count - 1]
        print(
            pad_right(m.name, 20)
            + " n "
            + pad_right(str(count), 4)
            + " mean "
            + pad_right(num(mean(values)), 8)
            + " last "
            + num(last.value)
            + " on "
            + day(last.at)
        )


def metric_detail(s: Session, name: str) -> None:
    """The mean, the range, control limits and the spec for one metric."""
    m = s.state.metric_index(name)
    if m < 0 or len(s.state.metrics[m].points) == 0:
        raise user_error("nothing logged for '" + name + "'")
    metric = s.state.metrics[m]
    values = [p.value for p in metric.points]
    count = len(values)
    first = metric.points[0]
    last = metric.points[count - 1]
    print(name + ": " + str(count) + " measurements, " + day(first.at) + " to " + day(last.at))
    low = values[0]
    high = values[0]
    for v in values:
        if v < low:
            low = v
        if v > high:
            high = v
    print(
        "mean     "
        + num(mean(values))
        + "   min "
        + num(low)
        + "   max "
        + num(high)
        + "   last "
        + num(last.value)
    )

    limits = control_limits(values)
    if limits is not None:
        print(
            "control  "
            + num(limits.lower)
            + " .. "
            + num(limits.upper)
            + "  (ordinary variation stays inside)"
        )
        outside = 0
        for p in metric.points:
            if not limits.holds(p.value):
                outside += 1
                print("  special cause: " + num(p.value) + " on " + day(p.at))
        if outside == 0:
            print(
                "  stable: no point has a special cause. Changing the result"
                + " means changing the process."
            )

    if metric.spec is not None:
        lo = metric.spec.lo
        hi = metric.spec.hi
        defects = sum(1 for v in values if v < lo or v > hi)
        print("spec     " + num(lo) + " .. " + num(hi) + ": " + str(defects) + " of " + str(count) + " outside")
    else:
        print("spec     none. Say what good looks like: vinci spec " + name + " <low> <high>")


def dispatch_knowledge(s: Session, cmd: str, args: list[str]) -> bool:
    if cmd == "bookmark":
        title = s.arg(args, 0, "title")
        url = s.arg(args, 1, "url")
        new_bookmark(s, title, url, tail(args, 2))
        return True
    if cmd == "unbookmark":
        unbookmark(s, s.arg(args, 0, "bookmark id"))
        return True
    if cmd == "bookmarks":
        bookmarks(s)
        return True
    if cmd == "source":
        citation = s.arg(args, 0, "citation")
        url = args[1] if len(args) > 1 else None
        new_source(s, citation, url)
        return True
    if cmd == "note":
        text = s.arg(args, 0, "text")
        new_note(s, text, tail(args, 1))
        return True
    if cmd == "link":
        from_id = s.arg(args, 0, "note id")
        to_id = s.arg(args, 1, "note or idea id")
        link(s, from_id, to_id)
        return True
    if cmd == "notes":
        query = args[0] if len(args) > 0 else None
        notes(s, query)
        return True
    if cmd == "tags":
        tags(s)
        return True
    if cmd == "sources":
        sources(s)
        return True
    if cmd == "show":
        show(s, s.arg(args, 0, "id"))
        return True
    if cmd == "predict":
        text = s.arg(args, 0, "what you expect")
        percent = number(s.arg(args, 1, "confidence percent"), "confidence percent")
        idea_id = args[2] if len(args) > 2 else None
        predict(s, text, percent, idea_id)
        return True
    if cmd == "result":
        ident = s.arg(args, 0, "prediction id")
        verdict = s.arg(args, 1, "right or wrong")
        outcome = s.arg(args, 2, "what happened")
        result(s, ident, verdict, outcome)
        return True
    if cmd == "predictions":
        predictions(s)
        return True
    if cmd == "log":
        metric = s.arg(args, 0, "metric")
        value = number(s.arg(args, 1, "value"), "value")
        comment = s.arg(args, 2, "comment") if len(args) > 2 else None
        log_value(s, metric, value, comment)
        return True
    if cmd == "spec":
        metric = s.arg(args, 0, "metric")
        lo = number(s.arg(args, 1, "low"), "low")
        hi = number(s.arg(args, 2, "high"), "high")
        spec(s, metric, lo, hi)
        return True
    if cmd == "stats":
        if len(args) == 0:
            metrics(s)
        else:
            metric_detail(s, args[0])
        return True
    return False
