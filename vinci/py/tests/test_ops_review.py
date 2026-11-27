"""The review loop's first findings and the next step (review.py)."""

from vinci_core.journal import Event
from vinci_core.review import findings, next_step, slowest_stage
from vinci_core.state import State, state_from
from vinci_core.timeutil import parse_instant


def test_one_issue_without_a_root_cause() -> None:
    t0 = parse_instant("2026-11-01T09:00:00+00:00")
    later = parse_instant("2026-11-16T09:00:00+00:00")
    state = state_from([Event(t0, "issue", id="x1", text="tests slip")])
    found = findings(state, later)
    assert len(found) == 1
    assert found[0].rank == 5
    assert found[0].subject == "x1"
    assert found[0].problem == "no root cause yet: tests slip"
    assert found[0].fix == 'vinci journal diagnose x1 "root cause"'
    assert next_step(state, later).what == "x1: no root cause yet: tests slip"


def test_empty_state_has_nothing_to_report() -> None:
    later = parse_instant("2026-11-16T09:00:00+00:00")
    empty = State()
    assert len(findings(empty, later)) == 0
    assert slowest_stage(empty, later) is None
    assert next_step(empty, later).what == "nothing is waiting on you"
