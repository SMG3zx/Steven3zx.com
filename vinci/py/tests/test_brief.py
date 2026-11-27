"""The daily brief: deterministic, cited, totals only for time."""

from vinci_core.brief import brief
from vinci_core.journal import Event
from vinci_core.state import state_from
from vinci_core.timeutil import TICKS_PER_DAY, Instant, parse_instant

NOW = parse_instant("2026-11-26T09:00:00+00:00")


def at(days_ago: float) -> Instant:
    return Instant(NOW.utc - int(days_ago * TICKS_PER_DAY), 0)


def make(events: list[Event]) -> str:
    return brief(events, state_from(events), NOW)


def test_empty_journal_says_so_in_every_section() -> None:
    text = make([])
    assert "none open" in text
    assert "nothing recorded" in text
    assert "nothing counted" in text


def test_open_commitment_is_shown_and_cited_by_day() -> None:
    commit_at = at(2)
    text = make([Event(commit_at, "commit", text="Build the wing frame")])
    assert "open: Build the wing frame [commit 2026-11-24]" in text


def test_a_recorded_fact_is_cited_by_its_id() -> None:
    text = make([Event(at(1), "idea", id="i1", text="Flying machine")])
    assert "idea: Flying machine [i1]" in text


def test_facts_older_than_a_week_are_left_out() -> None:
    text = make([Event(at(9), "idea", id="i1", text="Old idea")])
    assert "Old idea" not in text
    assert "nothing recorded" in text


def test_bookkeeping_is_not_listed_as_recorded() -> None:
    text = make(
        [
            Event(at(2), "idea", id="i1", text="Glider"),
            Event(at(1), "work", id="i1", text="commit subject", where_="abc123"),
        ]
    )
    assert "commit subject" not in text


def test_time_is_totals_per_idea_with_no_window_titles() -> None:
    events = [
        Event(at(1), "idea", id="i1", text="Glider"),
        Event(at(1), "spent", id="i1", value=90.0, where_="window"),
        Event(at(2), "spent", id="i1", value=30.0, where_="window"),
        Event(at(3), "spent", id="i1", value=45.0, where_="window"),
    ]
    text = make(events)
    assert "Glider: 2h45m [i1]" in text
    assert "Wing.cs" not in text


def test_time_older_than_a_week_is_not_counted() -> None:
    events = [
        Event(at(1), "idea", id="i1", text="Glider"),
        Event(at(10), "spent", id="i1", value=600.0, where_="window"),
    ]
    assert "nothing counted" in make(events)


def test_same_input_gives_same_text() -> None:
    events = [
        Event(at(1), "idea", id="i1", text="Glider"),
        Event(at(1), "spent", id="i1", value=30.0, where_="window"),
    ]
    assert make(events) == make(events)
