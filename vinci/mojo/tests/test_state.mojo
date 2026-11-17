# State tests: the fold over the journal. The c1 fixture is the calibration task's
# journal, read from tests/fixtures. The other cases are the state-level assertions
# of tests/VinciTests.cs, rewritten as events with explicit times (no App output).
# Times are written in +00:00 so the day numbers are easy to check.

from std.pathlib import Path
from std.testing import assert_equal, assert_true

from journal import Event, parse_event
from state import (
    BUILD,
    INCUBATE,
    PREPARE,
    RETIRED,
    SEED,
    TEND,
    State,
    home_pattern,
    stage_name,
    state_from,
)
from text import span
from timeutil import Instant, parse_instant

comptime DAY = 864000000000
comptime HOUR = 36000000000


def fold(lines: List[String]) raises -> State:
    var events = List[Event]()
    for line in lines:
        events.append(parse_event(line))
    return state_from(events)


def at(text: String) raises -> Instant:
    return parse_instant(text)


def fixture_state() raises -> State:
    var lines = List[String]()
    for line in Path("tests/fixtures/c1-journal.jsonl").read_text().split("\n"):
        if String(line).byte_length() > 0:
            lines.append(String(line))
    return fold(lines)


def test_c1_fixture() raises:
    var state = fixture_state()
    assert_equal(len(state.ideas), 6)
    var ids = List[String]()
    ids.append("i1")
    ids.append("i2")
    ids.append("i3")
    ids.append("i4")
    ids.append("i5")
    ids.append("i6")
    var stages = List[String]()
    stages.append("Build")
    stages.append("Tend")
    stages.append("Retired")
    stages.append("Tend")
    stages.append("Incubate")
    stages.append("Seed")
    var touch = List[String]()
    touch.append("1h15m")
    touch.append("1h0m")
    touch.append("1d1h")
    touch.append("2d2h")
    touch.append("59m")
    touch.append("0m")
    var titles = List[String]()
    titles.append("alpha")
    titles.append("beta ≥ gamma")
    titles.append('delta "quoted"')
    titles.append("eta")
    titles.append("theta")
    titles.append("iota")
    for i in range(6):
        assert_equal(state.ideas[i].id, ids[i])
        assert_equal(stage_name(state.ideas[i].stage), stages[i])
        assert_equal(span(state.ideas[i].touch), touch[i])
        assert_equal(state.ideas[i].title, titles[i])
    # The stage with a value: incubating for two days from its own time.
    assert_equal(
        state.ideas[0].incubate_until.value().utc,
        at("2026-11-02T10:06:00-06:00").utc + 2 * DAY,
    )
    # The bookmark and the review were both read.
    assert_equal(len(state.bookmarks), 1)
    assert_equal(state.bookmarks[0].url, "https://example.com/")
    assert_equal(state.bookmarks[0].tags[1], "y")
    assert_equal(state.metrics[0].name, "vinci.findings")
    print("test_c1_fixture: ok")


def test_time_and_work_per_stage() raises:
    # Seed 3 days (30 min of work), then Prepare 5 days (90 min), as in VinciTests.
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            '{"at":"2026-01-01T09:00:00+00:00","kind":"spent","id":"i1","value":30}',
            '{"at":"2026-01-04T09:00:00+00:00","kind":"stage","id":"i1","text":"Prepare"}',
            '{"at":"2026-01-04T09:00:00+00:00","kind":"spent","id":"i1","value":90}',
        ]
    )
    var now = at("2026-01-09T09:00:00+00:00")
    assert_equal(span(s.ideas[0].time_in(SEED, now)), "3d0h")
    assert_equal(span(s.ideas[0].time_in(PREPARE, now)), "5d0h")
    assert_equal(span(s.ideas[0].touch_in[SEED]), "30m")
    assert_equal(span(s.ideas[0].touch_in[PREPARE]), "1h30m")
    assert_equal(span(s.ideas[0].touch), "2h0m")
    assert_true(s.ideas[0].entered(PREPARE))
    assert_true(not s.ideas[0].entered(BUILD))
    print("test_time_and_work_per_stage: ok")


def test_ship_and_goal() raises:
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"goal","id":"i1","text":"Carry'
                ' one person 100 m","metric":"distance-i1","value":100}'
            ),
            (
                '{"at":"2026-01-02T09:00:00+00:00","kind":"ship","id":"i1","text":"Glider'
                ' model"}'
            ),
        ]
    )
    assert_equal(stage_name(s.ideas[0].stage), "Tend")
    assert_equal(len(s.ideas[0].ships), 1)
    assert_true(s.goal_met(0) is None)  # never measured
    var l1 = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"goal","id":"i1","text":"Carry'
                ' one person 100 m","metric":"distance-i1","value":100}'
            ),
            '{"at":"2026-01-02T09:00:00+00:00","kind":"log","id":"distance-i1","value":60}',
        ]
    )
    assert_equal(l1.goal_met(0).value(), False)
    var l2 = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"goal","id":"i1","text":"Carry'
                ' one person 100 m","metric":"distance-i1","value":100}'
            ),
            '{"at":"2026-01-02T09:00:00+00:00","kind":"log","id":"distance-i1","value":60}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"log","id":"distance-i1","value":120}',
        ]
    )
    assert_equal(l2.goal_met(0).value(), True)
    assert_equal(l2.metric_index("distance-i1"), 0)
    assert_equal(l2.ideas[0].goal_target(), "≥ 100")
    # A log names a metric, so it must not move the idea's last touch.
    assert_equal(
        l2.ideas[0].last_touched.utc, at("2026-01-01T09:00:00+00:00").utc
    )
    print("test_ship_and_goal: ok")


def test_goal_target_shapes() raises:
    var s = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"a"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"goal","id":"i1","text":"g","metric":"m","value":2,"hi":9.5}',
        ]
    )
    assert_equal(s.ideas[0].goal_target(), "2..9.5")
    var ceiling = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"a"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"goal","id":"i1","text":"g","metric":"m","hi":3}',
        ]
    )
    assert_equal(ceiling.ideas[0].goal_target(), "≤ 3")
    print("test_goal_target_shapes: ok")


def test_watches_and_retirement() raises:
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i2","text":"Bridge"}',
            '{"at":"2026-01-01T09:01:00+00:00","kind":"watch","id":"i1","text":"wing"}',
            (
                '{"at":"2026-01-01T09:02:00+00:00","kind":"watch","id":"i2","text":"-'
                ' bridge -"}'
            ),
            '{"at":"2026-01-01T09:03:00+00:00","kind":"unwatch","id":"i1","text":"WING"}',
            (
                '{"at":"2026-01-01T09:04:00+00:00","kind":"retire","id":"i2","text":"The'
                ' river moved"}'
            ),
        ]
    )
    assert_equal(len(s.watches), 1)
    assert_equal(s.watches[0].pattern, "- bridge -")
    assert_equal(
        s.watching_since.value().utc, at("2026-01-01T09:01:00+00:00").utc
    )
    # The retired idea's watch is no longer in force.
    assert_equal(len(s.all_watches()), 0)
    assert_equal(s.ideas[1].retired_why.value(), "The river moved")
    assert_equal(stage_name(s.ideas[1].stage), "Retired")
    print("test_watches_and_retirement: ok")


def test_home_and_commits() raises:
    var s = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"glider"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i2","text":"Bridge"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"home","id":"i1","text":"/home/x/repo/glider"}',
            (
                '{"at":"2026-01-01T09:01:00+00:00","kind":"work","id":"i1","text":"Start'
                ' both","where":"abc"}'
            ),
            (
                '{"at":"2026-01-01T09:01:00+00:00","kind":"work","id":"i2","text":"Start'
                ' both","where":"abc"}'
            ),
            '{"at":"2026-01-01T09:01:00+00:00","kind":"work","id":"i2","text":"Again","where":"abc"}',
        ]
    )
    assert_true(s.ideas[0].home is not None)
    assert_true(s.watched(0))
    assert_true(not s.watched(1))
    assert_true(s.has_commit("i1", "abc"))
    assert_true(s.has_commit("i2", "abc"))
    assert_true(not s.has_commit("i1", "zzz"))
    assert_equal(len(s.commits), 2)  # the repeated commit on i2 is kept once
    assert_equal(len(s.ideas[1].work), 2)
    var watches = s.all_watches()
    assert_equal(len(watches), 1)
    assert_equal(watches[0].pattern, "- glider -")
    # A trailing slash leaves an empty file name, so the pattern has two spaces.
    assert_equal(home_pattern("/home/x/glider/"), "-  -")
    print("test_home_and_commits: ok")


def test_uses_and_bookmarks() raises:
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            '{"at":"2026-01-02T09:00:00+00:00","kind":"used","id":"i1","text":"x"}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"used","id":"i1","text":"x"}',
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"bookmark","id":"b1","text":"Qwik'
                ' Docs","url":"https://qwik.dev/","why":"Official'
                ' docs","tags":["docs","qwik"]}'
            ),
            '{"at":"2026-01-01T09:00:00+00:00","kind":"bookmark","id":"b2","text":"Bun","url":"https://bun.sh/docs"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"unbookmark","id":"b1"}',
        ]
    )
    assert_equal(len(s.ideas[0].uses), 2)
    assert_equal(s.ideas[0].touch, 0)
    assert_equal(len(s.bookmarks), 1)
    assert_equal(s.bookmarks[0].id, "b2")
    assert_true(s.bookmarks[0].why is None)
    # A dropped bookmark's id is not handed out again.
    assert_equal(s.next_id("b"), "b3")
    assert_equal(s.next_id("i"), "i2")
    print("test_uses_and_bookmarks: ok")


def test_notes_sources_and_tags() raises:
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            '{"at":"2026-01-01T09:00:00+00:00","kind":"source","id":"s1","text":"Codex","url":"https://example.org/codex"}',
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"note","id":"n1","text":"A'
                " bird"
                ' steers","refs":["s1","i1"],"where":"f.7v","tags":["Water","flow"]}'
            ),
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"note","id":"n2","text":"So'
                ' a pilot could steer","refs":["n1"]}'
            ),
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"note","id":"n3","text":"Air'
                ' is thin water","tags":["flow"]}'
            ),
        ]
    )
    assert_equal(s.note_index("n1"), 0)
    assert_equal(s.notes[0].where_.value(), "f.7v")
    assert_equal(s.source_index("s1"), 0)
    assert_equal(s.sources[0].url.value(), "https://example.org/codex")
    var about_i1 = s.notes_about("i1")
    assert_equal(len(about_i1), 1)
    assert_equal(s.notes[about_i1[0]].id, "n1")
    assert_equal(len(s.notes_about("n1")), 1)
    assert_equal(len(s.notes[2].tags), 1)
    assert_true(s.has("s1"))
    assert_true(not s.has("x9"))
    # A note that connects to nothing is still a note, with no refs.
    assert_equal(len(s.notes[2].refs), 0)
    print("test_notes_sources_and_tags: ok")


def test_link_adds_refs() raises:
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"note","id":"n1","text":"Water'
                ' moves"}'
            ),
            '{"at":"2026-01-02T09:00:00+00:00","kind":"link","id":"n1","refs":["i1"]}',
        ]
    )
    assert_equal(len(s.notes[0].refs), 1)
    assert_equal(s.notes[0].refs[0], "i1")
    assert_equal(
        s.ideas[0].last_touched.utc, at("2026-01-02T09:00:00+00:00").utc
    )
    print("test_link_adds_refs: ok")


def test_predictions() raises:
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"predict","id":"h1","text":"A'
                ' 10 m wing lifts 80 kg","value":90,"refs":["i1"]}'
            ),
        ]
    )
    assert_equal(s.prediction_index("h1"), 0)
    assert_true(s.predictions[0].right is None)
    assert_equal(s.predictions[0].confidence, 90.0)
    assert_equal(s.predictions[0].idea_id.value(), "i1")
    var settled = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"predict","id":"h1","text":"A'
                ' 10 m wing lifts 80 kg","value":90}'
            ),
            (
                '{"at":"2026-01-02T09:00:00+00:00","kind":"result","id":"h1","text":"Lifted'
                ' 35 kg","value":0}'
            ),
        ]
    )
    assert_equal(settled.predictions[0].right.value(), False)
    assert_equal(settled.predictions[0].outcome.value(), "Lifted 35 kg")
    assert_true(settled.predictions[0].idea_id is None)
    print("test_predictions: ok")


def test_neglect_and_incubation() raises:
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            '{"at":"2026-01-01T09:00:00+00:00","kind":"stage","id":"i1","text":"Prepare"}',
            '{"at":"2026-01-02T09:00:00+00:00","kind":"stage","id":"i1","text":"Incubate","value":30}',
            (
                '{"at":"2026-01-03T09:00:00+00:00","kind":"tend","id":"i1","text":"kept'
                ' up"}'
            ),
            '{"at":"2026-01-03T09:00:00+00:00","kind":"log","id":"i1","value":3}',
        ]
    )
    assert_equal(
        s.ideas[0].incubate_until.value().utc,
        at("2026-01-02T09:00:00+00:00").utc + 30 * DAY,
    )
    assert_equal(
        s.ideas[0].last_touched.utc, at("2026-01-03T09:00:00+00:00").utc
    )
    assert_equal(len(s.ideas[0].stage_log), 3)
    # Entering the stage the idea is already in changes nothing.
    var again = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"a"}',
            '{"at":"2026-01-02T09:00:00+00:00","kind":"stage","id":"i1","text":"Seed"}',
        ]
    )
    assert_equal(len(again.ideas[0].stage_log), 1)
    print("test_neglect_and_incubation: ok")


def test_issues_principles_and_breaks() raises:
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            '{"at":"2026-01-01T09:00:00+00:00","kind":"stage","id":"i1","text":"Build"}',
            (
                '{"at":"2026-01-02T09:00:00+00:00","kind":"issue","id":"x1","text":"Wing'
                ' tore","refs":["i1"]}'
            ),
            (
                '{"at":"2026-01-02T09:00:00+00:00","kind":"diagnose","id":"x1","text":"Never'
                ' load-tested"}'
            ),
            (
                '{"at":"2026-01-02T09:00:00+00:00","kind":"principle","id":"p1","text":"Test'
                ' each material alone","refs":["x1"]}'
            ),
            (
                '{"at":"2026-01-03T09:00:00+00:00","kind":"issue","id":"x2","text":"Skipped'
                ' it again","value":1,"refs":["p1"]}'
            ),
            (
                '{"at":"2026-01-03T09:00:00+00:00","kind":"issue","id":"x3","text":"And'
                ' again","refs":["p1"]}'
            ),
        ]
    )
    assert_equal(stage_name(s.issues[0].stage.value()), "Build")
    assert_equal(s.issues[0].root_cause.value(), "Never load-tested")
    assert_equal(s.issues[0].principle_id.value(), "p1")
    assert_true(s.issues[1].big)
    assert_true(not s.issues[2].big)
    assert_equal(s.issues[1].broke_id.value(), "p1")
    assert_equal(s.issues[0].idea_id.value(), "i1")
    assert_equal(s.principles[0].issue_id.value(), "x1")
    assert_equal(stage_name(s.principles[0].stage.value()), "Build")
    assert_equal(s.breaks("p1"), 2)
    assert_equal(s.breaks("p9"), 0)
    print("test_issues_principles_and_breaks: ok")


def test_issue_counts_stage_by_when_it_happened() raises:
    # The stage is read when the issue is recorded, so a later stage change does not move it.
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            '{"at":"2026-01-01T09:00:00+00:00","kind":"stage","id":"i1","text":"Build"}',
            (
                '{"at":"2026-01-02T09:00:00+00:00","kind":"issue","id":"x1","text":"Wing'
                ' tore","refs":["i1"]}'
            ),
            '{"at":"2026-01-03T09:00:00+00:00","kind":"stage","id":"i1","text":"Tend"}',
        ]
    )
    assert_equal(stage_name(s.issues[0].stage.value()), "Build")
    assert_equal(stage_name(s.ideas[0].stage), "Tend")
    print("test_issue_counts_stage_by_when_it_happened: ok")


def test_commitments() raises:
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"commit","text":"Build'
                ' the wing frame","refs":["i1"]}'
            ),
        ]
    )
    assert_equal(s.open_commitment_index(), 0)
    assert_equal(s.commitments[0].idea_id.value(), "i1")
    var kept = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"commit","text":"Build'
                ' the wing frame"}'
            ),
            '{"at":"2026-01-08T09:00:00+00:00","kind":"kept"}',
        ]
    )
    assert_equal(kept.commitments[0].kept.value(), True)
    assert_equal(kept.open_commitment_index(), -1)
    var missed = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"commit","text":"Build'
                ' the wing frame"}'
            ),
            (
                '{"at":"2026-01-08T09:00:00+00:00","kind":"missed","text":"Spent'
                ' the week on the bridge"}'
            ),
        ]
    )
    assert_equal(missed.commitments[0].kept.value(), False)
    print("test_commitments: ok")


def test_reviews_and_measurements() raises:
    var s = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"review","value":0}',
            '{"at":"2026-01-02T09:00:00+00:00","kind":"review","value":2}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"spec","id":"sleep","value":7,"hi":9}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"log","id":"sleep","value":5}',
            '{"at":"2026-01-04T09:00:00+00:00","kind":"log","id":"sleep","value":8}',
        ]
    )
    assert_equal(s.metric_index("vinci.findings"), 0)
    assert_equal(len(s.metrics[0].points), 2)
    assert_equal(s.metrics[0].points[1].value, 2.0)
    var sleep = s.metric_index("sleep")
    assert_equal(len(s.metrics[sleep].points), 2)
    assert_equal(s.metrics[sleep].spec.value().lo, 7.0)
    assert_equal(s.metrics[sleep].spec.value().hi, 9.0)
    print("test_reviews_and_measurements: ok")


def test_metric_named_like_an_id() raises:
    # A metric may be named like an idea. Its log must not count as work on it.
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            '{"at":"2026-01-21T09:00:00+00:00","kind":"log","id":"i1","value":3}',
        ]
    )
    assert_equal(
        s.ideas[0].last_touched.utc, at("2026-01-01T09:00:00+00:00").utc
    )
    assert_equal(s.metric_index("i1"), 0)
    print("test_metric_named_like_an_id: ok")


def test_requirements() raises:
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            (
                '{"at":"2026-01-02T09:00:00+00:00","kind":"req","id":"r1","text":"Flaps'
                ' like a bird","refs":["i1"],"who":"Leonardo","why":"Birds do"}'
            ),
            (
                '{"at":"2026-01-03T09:00:00+00:00","kind":"cut","id":"r1","text":"Fixed'
                ' wings glide"}'
            ),
        ]
    )
    assert_equal(s.requirement_index("r1"), 0)
    assert_true(not s.requirements[0].live())
    assert_equal(s.requirements[0].cut_why.value(), "Fixed wings glide")
    assert_equal(s.requirements[0].idea_id, "i1")
    var live = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            (
                '{"at":"2026-01-02T09:00:00+00:00","kind":"req","id":"r1","text":"Lifts'
                ' 80 kg","refs":["i1"],"who":"Leonardo","why":"A person"}'
            ),
        ]
    )
    assert_true(live.requirements[0].live())
    assert_equal(
        live.ideas[0].last_touched.utc, at("2026-01-02T09:00:00+00:00").utc
    )
    print("test_requirements: ok")


def test_unknown_kinds_are_skipped() raises:
    var s = fold(
        [
            (
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying'
                ' machine"}'
            ),
            '{"at":"2026-01-05T09:00:00+00:00","kind":"from-a-newer-build","id":"i1"}',
        ]
    )
    assert_equal(len(s.ideas), 1)
    assert_equal(
        s.ideas[0].last_touched.utc, at("2026-01-05T09:00:00+00:00").utc
    )
    print("test_unknown_kinds_are_skipped: ok")


def test_refusals() raises:
    var raised = 0
    try:
        _ = fold(
            [
                '{"at":"2026-01-01T09:00:00+00:00","kind":"spent","id":"i9","value":5}'
            ]
        )
    except:
        raised += 1
    try:
        _ = fold(
            [
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"a"}',
                '{"at":"2026-01-01T09:00:00+00:00","kind":"stage","id":"i1","text":"Nowhere"}',
            ]
        )
    except:
        raised += 1
    try:
        _ = fold(
            [
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"a"}',
                '{"at":"2026-01-01T09:00:00+00:00","kind":"kept"}',
            ]
        )
    except:
        raised += 1
    try:
        _ = fold(
            [
                '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"a"}',
                '{"at":"2026-01-01T09:00:00+00:00","kind":"issue","id":"x1","text":"t","refs":["i7"]}',
            ]
        )
    except:
        raised += 1
    assert_equal(raised, 4)
    print("test_refusals: ok")


def test_next_ids() raises:
    var s = State()
    assert_equal(s.next_id("i"), "i1")
    assert_equal(s.next_id("h"), "h1")
    var raised = False
    try:
        _ = s.next_id("z")
    except:
        raised = True
    assert_true(raised)
    print("test_next_ids: ok")


def main() raises:
    test_c1_fixture()
    test_time_and_work_per_stage()
    test_ship_and_goal()
    test_goal_target_shapes()
    test_watches_and_retirement()
    test_home_and_commits()
    test_uses_and_bookmarks()
    test_notes_sources_and_tags()
    test_link_adds_refs()
    test_predictions()
    test_neglect_and_incubation()
    test_issues_principles_and_breaks()
    test_issue_counts_stage_by_when_it_happened()
    test_commitments()
    test_reviews_and_measurements()
    test_metric_named_like_an_id()
    test_requirements()
    test_unknown_kinds_are_skipped()
    test_refusals()
    test_next_ids()
    print("test_state: ok")
