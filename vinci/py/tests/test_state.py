"""State tests: the fold over the journal. The c1 fixture is the calibration task's journal (copied
under tests/fixtures). The other cases are the state-level assertions of the C# tests, rewritten as
events with explicit times. Times are written in +00:00 so the day numbers are easy to check."""

from pathlib import Path

import pytest

from vinci_core.journal import Event, parse_event
from vinci_core.state import (
    BUILD,
    PREPARE,
    SEED,
    State,
    home_pattern,
    stage_name,
    state_from,
)
from vinci_core.text import span
from vinci_core.timeutil import Instant, parse_instant

DAY = 864000000000
FIXTURE = Path(__file__).resolve().parent / "fixtures" / "c1-journal.jsonl"


def fold(lines: list[str]) -> State:
    return state_from([parse_event(line) for line in lines])


def at(text: str) -> Instant:
    return parse_instant(text)


def fixture_state() -> State:
    lines = [line for line in FIXTURE.read_text(encoding="utf-8").split("\n") if line != ""]
    return fold(lines)


ALPHA = '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying machine"}'


def test_c1_fixture() -> None:
    state = fixture_state()
    assert len(state.ideas) == 6
    ids = ["i1", "i2", "i3", "i4", "i5", "i6"]
    stages = ["Build", "Tend", "Retired", "Tend", "Incubate", "Seed"]
    touch = ["1h15m", "1h0m", "1d1h", "2d2h", "59m", "0m"]
    titles = ["alpha", "beta ≥ gamma", 'delta "quoted"', "eta", "theta", "iota"]
    for i in range(6):
        assert state.ideas[i].id == ids[i]
        assert stage_name(state.ideas[i].stage) == stages[i]
        assert span(state.ideas[i].touch) == touch[i]
        assert state.ideas[i].title == titles[i]
    # The stage with a value: incubating for two days from its own time.
    assert state.ideas[0].incubate_until.utc == at("2026-11-02T10:06:00-06:00").utc + 2 * DAY
    # The bookmark and the review were both read.
    assert len(state.bookmarks) == 1
    assert state.bookmarks[0].url == "https://example.com/"
    assert state.bookmarks[0].tags[1] == "y"
    assert state.metrics[0].name == "vinci.findings"


def test_time_and_work_per_stage() -> None:
    # Seed 3 days (30 min of work), then Prepare 5 days (90 min), as in the C# tests.
    s = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"Flying machine"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"spent","id":"i1","value":30}',
            '{"at":"2026-01-04T09:00:00+00:00","kind":"stage","id":"i1","text":"Prepare"}',
            '{"at":"2026-01-04T09:00:00+00:00","kind":"spent","id":"i1","value":90}',
        ]
    )
    now = at("2026-01-09T09:00:00+00:00")
    assert span(s.ideas[0].time_in(SEED, now)) == "3d0h"
    assert span(s.ideas[0].time_in(PREPARE, now)) == "5d0h"
    assert span(s.ideas[0].touch_in[SEED]) == "30m"
    assert span(s.ideas[0].touch_in[PREPARE]) == "1h30m"
    assert span(s.ideas[0].touch) == "2h0m"
    assert s.ideas[0].entered(PREPARE)
    assert not s.ideas[0].entered(BUILD)


def test_ship_and_goal() -> None:
    goal = '{"at":"2026-01-01T09:00:00+00:00","kind":"goal","id":"i1","text":"Carry one person 100 m","metric":"distance-i1","value":100}'
    s = fold([ALPHA, goal, '{"at":"2026-01-02T09:00:00+00:00","kind":"ship","id":"i1","text":"Glider model"}'])
    assert stage_name(s.ideas[0].stage) == "Tend"
    assert len(s.ideas[0].ships) == 1
    assert s.goal_met(0) is None  # never measured
    l1 = fold([ALPHA, goal, '{"at":"2026-01-02T09:00:00+00:00","kind":"log","id":"distance-i1","value":60}'])
    assert l1.goal_met(0) is False
    l2 = fold(
        [
            ALPHA,
            goal,
            '{"at":"2026-01-02T09:00:00+00:00","kind":"log","id":"distance-i1","value":60}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"log","id":"distance-i1","value":120}',
        ]
    )
    assert l2.goal_met(0) is True
    assert l2.metric_index("distance-i1") == 0
    assert l2.ideas[0].goal_target() == "≥ 100"
    # A log names a metric, so it must not move the idea's last touch.
    assert l2.ideas[0].last_touched.utc == at("2026-01-01T09:00:00+00:00").utc


def test_goal_target_shapes() -> None:
    s = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"a"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"goal","id":"i1","text":"g","metric":"m","value":2,"hi":9.5}',
        ]
    )
    assert s.ideas[0].goal_target() == "2..9.5"
    ceiling = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"a"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"goal","id":"i1","text":"g","metric":"m","hi":3}',
        ]
    )
    assert ceiling.ideas[0].goal_target() == "≤ 3"


def test_watches_and_retirement() -> None:
    s = fold(
        [
            ALPHA,
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i2","text":"Bridge"}',
            '{"at":"2026-01-01T09:01:00+00:00","kind":"watch","id":"i1","text":"wing"}',
            '{"at":"2026-01-01T09:02:00+00:00","kind":"watch","id":"i2","text":"- bridge -"}',
            '{"at":"2026-01-01T09:03:00+00:00","kind":"unwatch","id":"i1","text":"WING"}',
            '{"at":"2026-01-01T09:04:00+00:00","kind":"retire","id":"i2","text":"The river moved"}',
        ]
    )
    assert len(s.watches) == 1
    assert s.watches[0].pattern == "- bridge -"
    assert s.watching_since.utc == at("2026-01-01T09:01:00+00:00").utc
    # The retired idea's watch is no longer in force.
    assert len(s.all_watches()) == 0
    assert s.ideas[1].retired_why == "The river moved"
    assert stage_name(s.ideas[1].stage) == "Retired"


def test_home_and_commits() -> None:
    s = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"glider"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i2","text":"Bridge"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"home","id":"i1","text":"/home/x/repo/glider"}',
            '{"at":"2026-01-01T09:01:00+00:00","kind":"work","id":"i1","text":"Start both","where":"abc"}',
            '{"at":"2026-01-01T09:01:00+00:00","kind":"work","id":"i2","text":"Start both","where":"abc"}',
            '{"at":"2026-01-01T09:01:00+00:00","kind":"work","id":"i2","text":"Again","where":"abc"}',
        ]
    )
    assert s.ideas[0].home is not None
    assert s.watched(0)
    assert not s.watched(1)
    assert s.has_commit("i1", "abc")
    assert s.has_commit("i2", "abc")
    assert not s.has_commit("i1", "zzz")
    assert len(s.commits) == 2  # the repeated commit on i2 is kept once
    assert len(s.ideas[1].work) == 2
    watches = s.all_watches()
    assert len(watches) == 1
    assert watches[0].pattern == "- glider -"
    # A trailing slash leaves an empty file name, so the pattern has two spaces.
    assert home_pattern("/home/x/glider/") == "-  -"


def test_uses_and_bookmarks() -> None:
    s = fold(
        [
            ALPHA,
            '{"at":"2026-01-02T09:00:00+00:00","kind":"used","id":"i1","text":"x"}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"used","id":"i1","text":"x"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"bookmark","id":"b1","text":"Qwik Docs","url":"https://qwik.dev/","why":"Official docs","tags":["docs","qwik"]}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"bookmark","id":"b2","text":"Bun","url":"https://bun.sh/docs"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"unbookmark","id":"b1"}',
        ]
    )
    assert len(s.ideas[0].uses) == 2
    assert s.ideas[0].touch == 0
    assert len(s.bookmarks) == 1
    assert s.bookmarks[0].id == "b2"
    assert s.bookmarks[0].why is None
    # A dropped bookmark's id is not handed out again.
    assert s.next_id("b") == "b3"
    assert s.next_id("i") == "i2"


def test_notes_sources_and_tags() -> None:
    s = fold(
        [
            ALPHA,
            '{"at":"2026-01-01T09:00:00+00:00","kind":"source","id":"s1","text":"Codex","url":"https://example.org/codex"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"note","id":"n1","text":"A bird steers","refs":["s1","i1"],"where":"f.7v","tags":["Water","flow"]}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"note","id":"n2","text":"So a pilot could steer","refs":["n1"]}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"note","id":"n3","text":"Air is thin water","tags":["flow"]}',
        ]
    )
    assert s.note_index("n1") == 0
    assert s.notes[0].where_ == "f.7v"
    assert s.source_index("s1") == 0
    assert s.sources[0].url == "https://example.org/codex"
    about_i1 = s.notes_about("i1")
    assert len(about_i1) == 1
    assert s.notes[about_i1[0]].id == "n1"
    assert len(s.notes_about("n1")) == 1
    assert len(s.notes[2].tags) == 1
    assert s.has("s1")
    assert not s.has("x9")
    # A note that connects to nothing is still a note, with no refs.
    assert len(s.notes[2].refs) == 0


def test_link_adds_refs() -> None:
    s = fold(
        [
            ALPHA,
            '{"at":"2026-01-01T09:00:00+00:00","kind":"note","id":"n1","text":"Water moves"}',
            '{"at":"2026-01-02T09:00:00+00:00","kind":"link","id":"n1","refs":["i1"]}',
        ]
    )
    assert len(s.notes[0].refs) == 1
    assert s.notes[0].refs[0] == "i1"
    assert s.ideas[0].last_touched.utc == at("2026-01-02T09:00:00+00:00").utc


def test_predictions() -> None:
    s = fold(
        [
            ALPHA,
            '{"at":"2026-01-01T09:00:00+00:00","kind":"predict","id":"h1","text":"A 10 m wing lifts 80 kg","value":90,"refs":["i1"]}',
        ]
    )
    assert s.prediction_index("h1") == 0
    assert s.predictions[0].right is None
    assert s.predictions[0].confidence == 90.0
    assert s.predictions[0].idea_id == "i1"
    settled = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"predict","id":"h1","text":"A 10 m wing lifts 80 kg","value":90}',
            '{"at":"2026-01-02T09:00:00+00:00","kind":"result","id":"h1","text":"Lifted 35 kg","value":0}',
        ]
    )
    assert settled.predictions[0].right is False
    assert settled.predictions[0].outcome == "Lifted 35 kg"
    assert settled.predictions[0].idea_id is None


def test_neglect_and_incubation() -> None:
    s = fold(
        [
            ALPHA,
            '{"at":"2026-01-01T09:00:00+00:00","kind":"stage","id":"i1","text":"Prepare"}',
            '{"at":"2026-01-02T09:00:00+00:00","kind":"stage","id":"i1","text":"Incubate","value":30}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"tend","id":"i1","text":"kept up"}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"log","id":"i1","value":3}',
        ]
    )
    assert s.ideas[0].incubate_until.utc == at("2026-01-02T09:00:00+00:00").utc + 30 * DAY
    assert s.ideas[0].last_touched.utc == at("2026-01-03T09:00:00+00:00").utc
    assert len(s.ideas[0].stage_log) == 3
    # Entering the stage the idea is already in changes nothing.
    again = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"a"}',
            '{"at":"2026-01-02T09:00:00+00:00","kind":"stage","id":"i1","text":"Seed"}',
        ]
    )
    assert len(again.ideas[0].stage_log) == 1


def test_issues_principles_and_breaks() -> None:
    s = fold(
        [
            ALPHA,
            '{"at":"2026-01-01T09:00:00+00:00","kind":"stage","id":"i1","text":"Build"}',
            '{"at":"2026-01-02T09:00:00+00:00","kind":"issue","id":"x1","text":"Wing tore","refs":["i1"]}',
            '{"at":"2026-01-02T09:00:00+00:00","kind":"diagnose","id":"x1","text":"Never load-tested"}',
            '{"at":"2026-01-02T09:00:00+00:00","kind":"principle","id":"p1","text":"Test each material alone","refs":["x1"]}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"issue","id":"x2","text":"Skipped it again","value":1,"refs":["p1"]}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"issue","id":"x3","text":"And again","refs":["p1"]}',
        ]
    )
    assert stage_name(s.issues[0].stage) == "Build"
    assert s.issues[0].root_cause == "Never load-tested"
    assert s.issues[0].principle_id == "p1"
    assert s.issues[1].big
    assert not s.issues[2].big
    assert s.issues[1].broke_id == "p1"
    assert s.issues[0].idea_id == "i1"
    assert s.principles[0].issue_id == "x1"
    assert stage_name(s.principles[0].stage) == "Build"
    assert s.breaks("p1") == 2
    assert s.breaks("p9") == 0


def test_issue_counts_stage_by_when_it_happened() -> None:
    # The stage is read when the issue is recorded, so a later stage change does not move it.
    s = fold(
        [
            ALPHA,
            '{"at":"2026-01-01T09:00:00+00:00","kind":"stage","id":"i1","text":"Build"}',
            '{"at":"2026-01-02T09:00:00+00:00","kind":"issue","id":"x1","text":"Wing tore","refs":["i1"]}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"stage","id":"i1","text":"Tend"}',
        ]
    )
    assert stage_name(s.issues[0].stage) == "Build"
    assert stage_name(s.ideas[0].stage) == "Tend"


def test_commitments() -> None:
    s = fold(
        [
            ALPHA,
            '{"at":"2026-01-01T09:00:00+00:00","kind":"commit","text":"Build the wing frame","refs":["i1"]}',
        ]
    )
    assert s.open_commitment_index() == 0
    assert s.commitments[0].idea_id == "i1"
    kept = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"commit","text":"Build the wing frame"}',
            '{"at":"2026-01-08T09:00:00+00:00","kind":"kept"}',
        ]
    )
    assert kept.commitments[0].kept is True
    assert kept.open_commitment_index() == -1
    missed = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"commit","text":"Build the wing frame"}',
            '{"at":"2026-01-08T09:00:00+00:00","kind":"missed","text":"Spent the week on the bridge"}',
        ]
    )
    assert missed.commitments[0].kept is False


def test_reviews_and_measurements() -> None:
    s = fold(
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"review","value":0}',
            '{"at":"2026-01-02T09:00:00+00:00","kind":"review","value":2}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"spec","id":"sleep","value":7,"hi":9}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"log","id":"sleep","value":5}',
            '{"at":"2026-01-04T09:00:00+00:00","kind":"log","id":"sleep","value":8}',
        ]
    )
    assert s.metric_index("vinci.findings") == 0
    assert len(s.metrics[0].points) == 2
    assert s.metrics[0].points[1].value == 2.0
    sleep = s.metric_index("sleep")
    assert len(s.metrics[sleep].points) == 2
    assert s.metrics[sleep].spec.lo == 7.0
    assert s.metrics[sleep].spec.hi == 9.0


def test_metric_named_like_an_id() -> None:
    # A metric may be named like an idea. Its log must not count as work on it.
    s = fold(
        [
            ALPHA,
            '{"at":"2026-01-21T09:00:00+00:00","kind":"log","id":"i1","value":3}',
        ]
    )
    assert s.ideas[0].last_touched.utc == at("2026-01-01T09:00:00+00:00").utc
    assert s.metric_index("i1") == 0


def test_requirements() -> None:
    s = fold(
        [
            ALPHA,
            '{"at":"2026-01-02T09:00:00+00:00","kind":"req","id":"r1","text":"Flaps like a bird","refs":["i1"],"who":"Leonardo","why":"Birds do"}',
            '{"at":"2026-01-03T09:00:00+00:00","kind":"cut","id":"r1","text":"Fixed wings glide"}',
        ]
    )
    assert s.requirement_index("r1") == 0
    assert not s.requirements[0].live()
    assert s.requirements[0].cut_why == "Fixed wings glide"
    assert s.requirements[0].idea_id == "i1"
    live = fold(
        [
            ALPHA,
            '{"at":"2026-01-02T09:00:00+00:00","kind":"req","id":"r1","text":"Lifts 80 kg","refs":["i1"],"who":"Leonardo","why":"A person"}',
        ]
    )
    assert live.requirements[0].live()
    assert live.ideas[0].last_touched.utc == at("2026-01-02T09:00:00+00:00").utc


def test_unknown_kinds_are_skipped() -> None:
    s = fold(
        [
            ALPHA,
            '{"at":"2026-01-05T09:00:00+00:00","kind":"from-a-newer-build","id":"i1"}',
        ]
    )
    assert len(s.ideas) == 1
    assert s.ideas[0].last_touched.utc == at("2026-01-05T09:00:00+00:00").utc


@pytest.mark.parametrize(
    "lines",
    [
        ['{"at":"2026-01-01T09:00:00+00:00","kind":"spent","id":"i9","value":5}'],
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"a"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"stage","id":"i1","text":"Nowhere"}',
        ],
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"a"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"kept"}',
        ],
        [
            '{"at":"2026-01-01T09:00:00+00:00","kind":"idea","id":"i1","text":"a"}',
            '{"at":"2026-01-01T09:00:00+00:00","kind":"issue","id":"x1","text":"t","refs":["i7"]}',
        ],
    ],
)
def test_refusals(lines: list[str]) -> None:
    with pytest.raises(ValueError):
        fold(lines)


def test_next_ids() -> None:
    s = State()
    assert s.next_id("i") == "i1"
    assert s.next_id("h") == "h1"
    with pytest.raises(ValueError):
        s.next_id("z")
