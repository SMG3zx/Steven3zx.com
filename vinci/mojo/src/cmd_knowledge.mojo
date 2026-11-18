# The knowledge group (App.cs "Knowledge", "Inquiry", "Measurement"). Owns these
# `case` labels of Dispatch: bookmark, unbookmark, bookmarks, source, note, link, notes,
# tags, sources, show, predict, result, predictions, log, spec, stats.
#
# show is the dispatcher for every id: an idea id goes to show_idea in cmd_ideas,
# and the other ids (note, source, issue, prediction) are written here.
#
# `args` is the list after the command name (C#'s `a`). Return True when the command
# was handled, False to let the next group try it.
#
# Known gaps: ToLowerInvariant and OrdinalIgnoreCase are ASCII-only here, so a
# non-ASCII tag or a non-ASCII search term is not folded the way .NET folds it.

from journal import Event
from jsonline import ascii_lower
from session import Session, note_line, number, prediction_line, user_error
from state import FINDINGS_METRIC, RETIRED, stage_name
from stats import control_limits, mean
from text import ascii_tail, day, num, pad_right
from timeutil import ascii_prefix
from cmd_ideas import show_idea


@fieldwise_init
struct TagCount(Copyable, Movable):
    """One subject in the tag index, and how many notes carry it."""

    var name: String
    var count: Int


def tail(a: List[String], start: Int) -> List[String]:
    """a[start..] as C# slices it: empty when start is past the end."""
    var out = List[String]()
    for i in range(start, len(a)):
        out.append(a[i])
    return out^


def colon_index(s: String) -> Int:
    """The byte index of the first colon, or -1."""
    var b = s.as_bytes()
    for i in range(len(b)):
        if b[i] == 58:  # :
            return i
    return -1


def is_web_address(url: String) -> Bool:
    """Portal.IsWebAddress: an absolute http or https address with something after
    the scheme."""
    var lower = ascii_lower(url)
    var rest: String
    if lower.startswith("http://"):
        rest = ascii_tail(url, 7)
    elif lower.startswith("https://"):
        rest = ascii_tail(url, 8)
    else:
        return False
    return rest.byte_length() > 0


def contains_ignore_case(hay: String, needle: String) -> Bool:
    """String.Contains with OrdinalIgnoreCase, for ASCII. An empty needle matches.
    """
    var hl = ascii_lower(hay)
    var nl = ascii_lower(needle)
    var h = hl.as_bytes()
    var n = nl.as_bytes()
    if len(n) == 0:
        return True
    if len(n) > len(h):
        return False
    for i in range(len(h) - len(n) + 1):
        var same = True
        for j in range(len(n)):
            if h[i + j] != n[j]:
                same = False
                break
        if same:
            return True
    return False


def linkable(mut s: Session, id: String) -> Bool:
    """Linkable: a note, idea, source or prediction that exists. The first letter
    says which kind, so only n, i, s and h are tried."""
    var b = id.as_bytes()
    if len(b) == 0:
        return False
    var first = b[0]
    if first != 110 and first != 105 and first != 115 and first != 104:
        return False  # n i s h
    return s.state.has(id)


def note_lines_about(mut s: Session, id: String) raises -> List[String]:
    """The note lines of every note that refers to id, in journal order."""
    var out = List[String]()
    for i in s.state.notes_about(id):
        out.append(note_line(s.state.notes[i]))
    return out^


def new_bookmark(
    mut s: Session, title: String, url: String, rest: List[String]
) raises:
    """NewBookmark: a saved link. +words are tags, the rest is why it is kept.
    """
    if not is_web_address(url):
        raise user_error("'" + url + "' is not an http or https address")
    for b in s.state.bookmarks:
        if b.url == url:
            raise user_error(b.id + " already points there: " + b.title)
    var tags = List[String]()
    var why_words = List[String]()
    for r in rest:
        if r.startswith("+"):
            if r.byte_length() > 1:
                tags.append(ascii_tail(r, 1))
        else:
            why_words.append(r)
    var why = String()
    for k in range(len(why_words)):
        if k > 0:
            why += " "
        why += why_words[k]
    var id = s.state.next_id("b")
    var tags_opt: Optional[List[String]] = None
    if len(tags) > 0:
        tags_opt = Optional[List[String]](tags^)
    var why_opt: Optional[String] = None
    if why.byte_length() > 0:
        why_opt = Optional[String](why^)
    s.record(
        Event(
            s.now.copy(),
            "bookmark",
            id=id.copy(),
            text=title.copy(),
            url=url.copy(),
            why=why_opt^,
            tags=tags_opt^,
        )
    )
    print(id + " saved. It is on the portal: vinci serve")


def unbookmark(mut s: Session, id: String) raises:
    """Unbookmark: take a saved link off the portal."""
    if not has_bookmark(s, id):
        raise user_error("no bookmark " + id)
    s.record(Event(s.now.copy(), "unbookmark", id=id.copy()))
    print(id + " is off the portal.")


def has_bookmark(mut s: Session, id: String) -> Bool:
    for b in s.state.bookmarks:
        if b.id == id:
            return True
    return False


def bookmarks(mut s: Session) raises:
    """Bookmarks: every saved link, one per line."""
    if len(s.state.bookmarks) == 0:
        print(
            'No bookmarks yet. vinci bookmark "title" <url> ["what it is for"]'
            " [+tag...]"
        )
        return
    for b in s.state.bookmarks:
        var line = pad_right(b.id, 4) + " " + b.title + "  " + b.url
        for t in b.tags:
            line += " +" + t
        if b.why is not None:
            line += "  (" + b.why.value() + ")"
        print(line)


def new_source(mut s: Session, citation: String, url: Optional[String]) raises:
    """NewSource: a place knowledge came from. Notes cite it by id."""
    var id = s.state.next_id("s")
    s.record(
        Event(
            s.now.copy(),
            "source",
            id=id.copy(),
            text=citation.copy(),
            url=url.copy(),
        )
    )
    print(
        id
        + ' recorded. Cite it with the place: vinci note "one idea, in your own'
        + ' words" '
        + id
        + ":p.12"
    )


def new_note(mut s: Session, text: String, rest: List[String]) raises:
    """NewNote: after the text come, in any order, ids to link to, a source with an
    optional place (s1:p.42), and tags (+flight)."""
    var refs = List[String]()
    var tags = List[String]()
    var where_: Optional[String] = None
    for token in rest:
        if token.startswith("+") and token.byte_length() > 1:
            tags.append(ascii_lower(ascii_tail(token, 1)))
            continue
        var id = token.copy()
        var colon = colon_index(token)
        var place: Optional[String] = None
        if colon >= 0:
            id = ascii_prefix(token, colon)
            place = Optional[String](ascii_tail(token, colon + 1))
        if not linkable(s, id):
            raise user_error(
                "'"
                + token
                + "' is not a note, idea, source or prediction that exists,"
                + " nor a +tag"
            )
        if place is not None:
            if id.as_bytes()[0] != 115:  # s
                raise user_error(
                    "only a source has a place inside it, and "
                    + id
                    + " is not a source"
                )
            where_ = place^
        refs.append(id^)
    var note_id = s.state.next_id("n")
    var has_source = False
    for r in refs:
        if r.as_bytes()[0] == 115:  # s
            has_source = True
    var refs_opt: Optional[List[String]] = None
    if len(refs) > 0:
        refs_opt = Optional[List[String]](refs^)
    var tags_opt: Optional[List[String]] = None
    if len(tags) > 0:
        tags_opt = Optional[List[String]](tags^)
    s.record(
        Event(
            s.now.copy(),
            "note",
            id=note_id.copy(),
            text=text.copy(),
            refs=refs_opt^,
            tags=tags_opt^,
            where_=where_^,
        )
    )
    var origin = String()
    if not has_source:
        origin = " No source given, so it is recorded as your own thought."
    print(note_id + " written." + origin)


def link(mut s: Session, from_id: String, to_id: String) raises:
    """Link: a note points at another note, idea, source or prediction."""
    var n = s.state.note_index(from_id)
    if n < 0:
        raise user_error("no note " + from_id)
    if not linkable(s, to_id):
        raise user_error(
            "'"
            + to_id
            + "' is not a note, idea, source or prediction that exists"
        )
    if from_id == to_id:
        raise user_error("a note cannot link to itself")
    for r in s.state.notes[n].refs:
        if r == to_id:
            raise user_error(from_id + " already links to " + to_id)
    var to_refs = List[String]()
    to_refs.append(to_id.copy())
    s.record(
        Event(
            s.now.copy(),
            "link",
            id=from_id.copy(),
            refs=Optional[List[String]](to_refs^),
        )
    )
    print(from_id + " → " + to_id)


def notes(mut s: Session, query: Optional[String]) raises:
    """Notes: every note, or those matching a word in the text or a +tag."""
    var found = 0
    for note in s.state.notes:
        var keep = True
        if query is not None:
            var q = query.value()
            if q.startswith("+"):
                keep = False
                var want = ascii_lower(ascii_tail(q, 1))
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


def tags(mut s: Session) raises:
    """Tags: the index of subjects, most used first, then by name."""
    var index = List[TagCount]()
    for note in s.state.notes:
        for t in note.tags:
            var at = -1
            for k in range(len(index)):
                if index[k].name == t:
                    at = k
                    break
            if at >= 0:
                index[at].count += 1
            else:
                index.append(TagCount(t.copy(), 1))
    if len(index) == 0:
        print('No tags yet. Add one to a note: vinci note "..." +subject')
        return
    # Insertion sort: count descending, then name ordinal. Names are unique.
    for i in range(1, len(index)):
        var j = i
        while j > 0 and (
            index[j].count > index[j - 1].count
            or (
                index[j].count == index[j - 1].count
                and index[j].name < index[j - 1].name
            )
        ):
            var swap = index[j].copy()
            index[j] = index[j - 1].copy()
            index[j - 1] = swap^
            j -= 1
    for tag in index:
        print("+" + pad_right(tag.name, 20) + " " + String(tag.count))


def sources(mut s: Session) raises:
    """Sources: every source with its url and how many notes cite it."""
    if len(s.state.sources) == 0:
        print('No sources yet. vinci source "citation" [url]')
        return
    for src in s.state.sources:
        var line = pad_right(src.id, 4) + " " + src.citation
        if src.url is not None:
            line += " <" + src.url.value() + ">"
        line += "  (" + String(len(s.state.notes_about(src.id))) + " notes)"
        print(line)


def show_note(mut s: Session, n: Int) raises:
    """ShowNote: the full view of one note."""
    var note = s.state.notes[n].copy()
    print(note.id + "  " + note.text)
    print("written  " + day(note.at))
    if len(note.tags) > 0:
        var line = "tags     "
        for k in range(len(note.tags)):
            if k > 0:
                line += " "
            line += "+" + note.tags[k]
        print(line)
    var cited = False
    for r in note.refs:
        if r.as_bytes()[0] == 115:  # s
            cited = True
            var si = s.state.source_index(r)
            if si < 0:
                raise Error(
                    "journal: note cites a source that does not exist: " + r
                )
            var src = s.state.sources[si].copy()
            var line = "origin   " + src.id + " " + src.citation
            if note.where_ is not None:
                line += ", " + note.where_.value()
            if src.url is not None:
                line += " <" + src.url.value() + ">"
            print(line)
    if not cited:
        print("origin   your own thought")
    var links = List[String]()
    for r in note.refs:
        if r.as_bytes()[0] != 115:  # not s
            links.append(s.describe(r))
    s.section("links to", links^)
    s.section("linked from", note_lines_about(s, note.id))


def show_issue(mut s: Session, x: Int) raises:
    """Show, for an issue: what went wrong, its cause and its principle."""
    var issue = s.state.issues[x].copy()
    print(issue.id + "  " + issue.text + ("  (big)" if issue.big else ""))
    var raised = "raised      " + day(issue.at)
    if issue.idea_id is not None:
        var stage = String()
        if issue.stage is not None:
            stage = stage_name(issue.stage.value())
        raised += " on " + issue.idea_id.value() + " in " + stage
    print(raised)
    if issue.broke_id is not None:
        var p = s.state.principle_index(issue.broke_id.value())
        if p < 0:
            raise Error("journal: issue names a principle that does not exist")
        print(
            "broke       "
            + issue.broke_id.value()
            + " "
            + s.state.principles[p].text
        )
    var cause = String("(not diagnosed)")
    if issue.root_cause is not None:
        cause = issue.root_cause.value()
    print("root cause  " + cause)
    var principle_line = String("(none yet)")
    if issue.principle_id is not None:
        var p = s.state.principle_index(issue.principle_id.value())
        if p >= 0:
            principle_line = (
                s.state.principles[p].id + " " + s.state.principles[p].text
            )
    print("principle   " + principle_line)


def show_prediction(mut s: Session, h: Int) raises:
    """Show, for a prediction: the claim, when it was made, and the outcome."""
    var pred = s.state.predictions[h].copy()
    print(prediction_line(pred))
    var made = "made     " + day(pred.at)
    if pred.idea_id is not None:
        made += " about " + pred.idea_id.value()
    print(made)
    if pred.outcome is not None:
        print("outcome  " + pred.outcome.value())
    s.section("notes about it", note_lines_about(s, pred.id))


def show_source(mut s: Session, si: Int) raises:
    """Show, for a source: its citation, url, date, and the notes drawn from it.
    """
    var src = s.state.sources[si].copy()
    print(src.id + "  " + src.citation)
    if src.url is not None:
        print("url       " + src.url.value())
    print("recorded  " + day(src.at))
    s.section("notes drawn from it", note_lines_about(s, src.id))


def show(mut s: Session, id: String) raises:
    """Show: the view of any id. Ideas go to show_idea; the rest are written here.
    """
    var i = s.state.idea_index(id)
    if i >= 0:
        show_idea(s, i)
        return
    var n = s.state.note_index(id)
    if n >= 0:
        show_note(s, n)
        return
    var si = s.state.source_index(id)
    if si >= 0:
        show_source(s, si)
        return
    var x = s.state.issue_index(id)
    if x >= 0:
        show_issue(s, x)
        return
    var h = s.state.prediction_index(id)
    if h >= 0:
        show_prediction(s, h)
        return
    raise user_error("nothing has the id '" + id + "'")


def predict(
    mut s: Session, text: String, percent: Float64, idea_id: Optional[String]
) raises:
    """Predict: write down what you expect and how sure you are, before the test.
    """
    if percent < 1.0 or percent > 99.0:
        raise user_error("confidence is a percent from 1 to 99")
    if idea_id is not None and s.state.idea_index(idea_id.value()) < 0:
        raise user_error("no idea " + idea_id.value())
    var id = s.state.next_id("h")
    var refs_opt: Optional[List[String]] = None
    if idea_id is not None:
        var refs = List[String]()
        refs.append(idea_id.value())
        refs_opt = Optional[List[String]](refs^)
    s.record(
        Event(
            s.now.copy(),
            "predict",
            id=id.copy(),
            text=text.copy(),
            refs=refs_opt^,
            value=percent,
        )
    )
    print(
        id
        + " predicted at "
        + num(percent)
        + "%. Test it, then: vinci result "
        + id
        + ' right|wrong "what happened"'
    )


def result(mut s: Session, id: String, verdict: String, outcome: String) raises:
    """Result: settle a prediction, right or wrong, with what happened."""
    var h = s.state.prediction_index(id)
    if h < 0:
        raise user_error("no prediction " + id)
    if s.state.predictions[h].right is not None:
        raise user_error(id + " is already settled")
    if verdict != "right" and verdict != "wrong":
        raise user_error("say right or wrong, not '" + verdict + "'")
    var value = 0.0
    if verdict == "right":
        value = 1.0
    s.record(
        Event(
            s.now.copy(),
            "result",
            id=id.copy(),
            text=outcome.copy(),
            value=value,
        )
    )
    print(
        id
        + " was "
        + verdict
        + '. Keep what you learned: vinci note "..." '
        + id
    )


def predictions(mut s: Session) raises:
    """Predictions: every prediction, then your calibration once some are settled.
    """
    if len(s.state.predictions) == 0:
        print(
            'No predictions yet. vinci predict "what you expect" <percent>'
            " [idea]"
        )
        return
    for h in s.state.predictions:
        print(prediction_line(h))

    var settled = 0
    var right_count = 0
    var confidence_sum = 0.0
    for h in s.state.predictions:
        if h.right is not None:
            settled += 1
            if h.right.value():
                right_count += 1
            confidence_sum += h.confidence
    if settled == 0:
        return
    var right = 100.0 * Float64(right_count) / Float64(settled)
    var sure = confidence_sum / Float64(settled)
    var enough = 10
    var verdict: String
    if settled < enough:
        verdict = (
            "Too few to judge ("
            + String(settled)
            + " of "
            + String(enough)
            + ")."
        )
    elif abs(sure - right) < 10.0:
        verdict = "Your confidence matches your record."
    elif sure > right:
        verdict = "You are overconfident by " + num(sure - right) + " points."
    else:
        verdict = "You are underconfident by " + num(right - sure) + " points."
    print(
        "Settled "
        + String(settled)
        + ": right "
        + num(right)
        + "%, average confidence "
        + num(sure)
        + "%. "
        + verdict
    )


def log_value(
    mut s: Session, metric: String, value: Float64, comment: Optional[String]
) raises:
    """Log: one measurement, and whether each goal on this metric is now met."""
    if metric == FINDINGS_METRIC:
        raise user_error(metric + " is kept by vinci itself")
    s.record(
        Event(
            s.now.copy(),
            "log",
            id=metric.copy(),
            text=comment.copy(),
            value=value,
        )
    )
    var n = 1
    var m = s.state.metric_index(metric)
    if m >= 0:
        n = len(s.state.metrics[m].points) + 1
    print(metric + " = " + num(value) + " (measurement " + String(n) + ").")
    for i in range(len(s.state.ideas)):
        var idea = s.state.ideas[i].copy()
        if idea.goal_metric is None or idea.goal_metric.value() != metric:
            continue
        if idea.stage == RETIRED:
            continue
        var met = True
        if idea.goal_min is not None and not (value >= idea.goal_min.value()):
            met = False
        if idea.goal_max is not None and not (value <= idea.goal_max.value()):
            met = False
        var verdict = "not met"
        if met:
            verdict = "met"
        print(
            idea.id + " goal " + verdict + ": needs " + idea.goal_target() + "."
        )


def spec(mut s: Session, metric: String, lo: Float64, hi: Float64) raises:
    """Spec: say what good looks like for a metric, between two limits."""
    if lo > hi:
        raise user_error("low must not be above high")
    s.record(
        Event(
            s.now.copy(),
            "spec",
            id=metric.copy(),
            value=lo,
            hi=hi,
        )
    )
    print(metric + " is good between " + num(lo) + " and " + num(hi) + ".")


def metrics(mut s: Session) raises:
    """Stats with no name: one line per measured metric."""
    if len(s.state.metrics) == 0:
        print("Nothing measured yet. vinci log <metric> <value>")
        return
    for m in s.state.metrics:
        var count = len(m.points)
        if count == 0:
            continue
        var values = List[Float64]()
        for p in m.points:
            values.append(p.value)
        var last = m.points[count - 1].copy()
        print(
            pad_right(m.name, 20)
            + " n "
            + pad_right(String(count), 4)
            + " mean "
            + pad_right(num(mean(values)), 8)
            + " last "
            + num(last.value)
            + " on "
            + day(last.at)
        )


def metric_detail(mut s: Session, name: String) raises:
    """Stats NAME: the mean, the range, control limits and the spec for one metric.
    """
    var m = s.state.metric_index(name)
    if m < 0 or len(s.state.metrics[m].points) == 0:
        raise user_error("nothing logged for '" + name + "'")
    var metric = s.state.metrics[m].copy()
    var values = List[Float64]()
    for p in metric.points:
        values.append(p.value)
    var count = len(values)
    var first = metric.points[0].copy()
    var last = metric.points[count - 1].copy()
    print(
        name
        + ": "
        + String(count)
        + " measurements, "
        + day(first.at)
        + " to "
        + day(last.at)
    )
    var low = values[0]
    var high = values[0]
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

    var limits = control_limits(values)
    if limits is not None:
        var lim = limits.value().copy()
        print(
            "control  "
            + num(lim.lower)
            + " .. "
            + num(lim.upper)
            + "  (ordinary variation stays inside)"
        )
        var outside = 0
        for p in metric.points:
            if not lim.holds(p.value):
                outside += 1
                print("  special cause: " + num(p.value) + " on " + day(p.at))
        if outside == 0:
            print(
                "  stable: no point has a special cause. Changing the result"
                + " means changing the process."
            )

    if metric.spec is not None:
        var lo = metric.spec.value().lo
        var hi = metric.spec.value().hi
        var defects = 0
        for v in values:
            if v < lo or v > hi:
                defects += 1
        print(
            "spec     "
            + num(lo)
            + " .. "
            + num(hi)
            + ": "
            + String(defects)
            + " of "
            + String(count)
            + " outside"
        )
    else:
        print(
            "spec     none. Say what good looks like: vinci spec "
            + name
            + " <low> <high>"
        )


def dispatch_knowledge(
    mut s: Session, cmd: String, args: List[String]
) raises -> Bool:
    if cmd == "bookmark":
        var title = s.arg(args, 0, "title")
        var url = s.arg(args, 1, "url")
        new_bookmark(s, title, url, tail(args, 2))
        return True
    if cmd == "unbookmark":
        unbookmark(s, s.arg(args, 0, "bookmark id"))
        return True
    if cmd == "bookmarks":
        bookmarks(s)
        return True
    if cmd == "source":
        var citation = s.arg(args, 0, "citation")
        var url: Optional[String] = None
        if len(args) > 1:
            url = Optional[String](args[1].copy())
        new_source(s, citation, url^)
        return True
    if cmd == "note":
        var text = s.arg(args, 0, "text")
        new_note(s, text, tail(args, 1))
        return True
    if cmd == "link":
        var from_id = s.arg(args, 0, "note id")
        var to_id = s.arg(args, 1, "note or idea id")
        link(s, from_id, to_id)
        return True
    if cmd == "notes":
        var query: Optional[String] = None
        if len(args) > 0:
            query = Optional[String](args[0].copy())
        notes(s, query^)
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
        var text = s.arg(args, 0, "what you expect")
        var percent = number(
            s.arg(args, 1, "confidence percent"), "confidence percent"
        )
        var idea_id: Optional[String] = None
        if len(args) > 2:
            idea_id = Optional[String](args[2].copy())
        predict(s, text, percent, idea_id^)
        return True
    if cmd == "result":
        var id = s.arg(args, 0, "prediction id")
        var verdict = s.arg(args, 1, "right or wrong")
        var outcome = s.arg(args, 2, "what happened")
        result(s, id, verdict, outcome)
        return True
    if cmd == "predictions":
        predictions(s)
        return True
    if cmd == "log":
        var metric = s.arg(args, 0, "metric")
        var value = number(s.arg(args, 1, "value"), "value")
        var comment: Optional[String] = None
        if len(args) > 2:
            comment = Optional[String](s.arg(args, 2, "comment"))
        log_value(s, metric, value, comment^)
        return True
    if cmd == "spec":
        var metric = s.arg(args, 0, "metric")
        var lo = number(s.arg(args, 1, "low"), "low")
        var hi = number(s.arg(args, 2, "high"), "high")
        spec(s, metric, lo, hi)
        return True
    if cmd == "stats":
        if len(args) == 0:
            metrics(s)
        else:
            metric_detail(s, args[0])
        return True
    return False
