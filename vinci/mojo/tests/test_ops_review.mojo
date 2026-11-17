from std.testing import assert_equal, assert_true

from journal import Event
from review import findings, next_step, slowest_stage
from state import State, state_from
from timeutil import parse_instant


def main() raises:
    var t0 = parse_instant("2026-11-01T09:00:00+00:00")
    var later = parse_instant("2026-11-16T09:00:00+00:00")

    # One issue with no root cause: rank 5, subject is the issue id.
    var events = List[Event]()
    events.append(
        Event(
            t0.copy(),
            "issue",
            id=String("x1"),
            text=String("tests slip"),
        )
    )
    var state = state_from(events)
    var found = findings(state, later)
    assert_equal(len(found), 1)
    assert_equal(found[0].rank, 5)
    assert_equal(found[0].subject, "x1")
    assert_equal(found[0].problem, "no root cause yet: tests slip")
    assert_equal(found[0].fix, 'vinci diagnose x1 "root cause"')

    var step = next_step(state, later)
    assert_equal(step.what, "x1: no root cause yet: tests slip")

    # No ideas and no measurements: nothing to report.
    var empty = State()
    assert_equal(len(findings(empty, later)), 0)
    assert_true(slowest_stage(empty, later) is None)
    assert_equal(next_step(empty, later).what, "nothing is waiting on you")
    print("test_ops_review: ok")
