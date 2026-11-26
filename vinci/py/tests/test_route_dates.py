"""Query routing, the retrieval plan and the date windows, against a fixed today. Today is
2026-11-16, a Monday, so "last week" is 2026-11-09 to 2026-11-16 (exclusive)."""


from vinci_core.dates import MODE_AFTER, MODE_BEFORE, MODE_IN, date_boost, parse_query_dates, window_matches
from vinci_core.fusion import DATE_BOOST
from vinci_core.route import plan_of, route_query, word_tokens

TODAY = "2026-11-16"


def expect_window(windows, index, mode, start, end) -> None:  # type: ignore[no-untyped-def]
    assert windows[index].mode == mode
    assert windows[index].start == start
    assert windows[index].end == end


def test_routes_follow_retrieval_cs() -> None:
    assert route_query("When did the portal move?") == "temporal"
    assert route_query("What was the latest plan?") == "temporal"
    assert route_query("How do I deploy the site?") == "procedural"
    assert route_query("Who owns the GoMath repo?") == "graph"
    assert route_query("Where does the code live?") == "semantic"
    # Whole words only: "steps" does not match "stepson", and "fix" not "fixture".
    assert route_query("Tell me about my stepson") == "semantic"
    assert route_query("the fixture set") == "semantic"
    # Temporal wins over procedural, as in RouteQuery.
    assert route_query("How do I deploy the latest build?") == "temporal"


def test_word_tokens_split_on_anything_but_word_bytes() -> None:
    assert word_tokens("Auto-indexed foo_bar, x1!") == ["auto", "indexed", "foo_bar", "x1"]


def test_plan_multiplies_candidates_by_complexity() -> None:
    simple = plan_of("Where does the code live?", "semantic")
    assert simple.complexity == "simple"
    assert simple.candidate_multiplier == 2
    temporal = plan_of("When did it change?", "temporal")
    assert temporal.complexity == "temporal"
    assert temporal.candidate_multiplier == 4
    multi = plan_of("Why do the two services depend on each other?", "semantic")
    assert multi.complexity == "multi-hop"
    assert multi.candidate_multiplier == 6


def test_last_june_is_the_june_before_this_month() -> None:
    windows = parse_query_dates("What was the weather in Lisbon last June?", TODAY)
    assert len(windows) == 1
    expect_window(windows, 0, MODE_IN, "2026-06-01", "2026-07-01")
    assert windows[0].phrase == "last june"


def test_last_june_in_june_means_last_year() -> None:
    expect_window(parse_query_dates("last June", "2026-06-15"), 0, MODE_IN, "2025-06-01", "2025-07-01")


def test_in_october_is_this_year_once_october_has_begun() -> None:
    expect_window(parse_query_dates("What happened in October?", TODAY), 0, MODE_IN, "2026-10-01", "2026-11-01")
    expect_window(parse_query_dates("in October", "2026-09-03"), 0, MODE_IN, "2025-10-01", "2025-11-01")


def test_month_and_year_name_their_year() -> None:
    expect_window(parse_query_dates("the plan for March 2024", TODAY), 0, MODE_IN, "2024-03-01", "2024-04-01")
    expect_window(parse_query_dates("in December 2025", TODAY), 0, MODE_IN, "2025-12-01", "2026-01-01")


def test_yesterday_and_today() -> None:
    windows = parse_query_dates("yesterday and today", TODAY)
    assert len(windows) == 2
    expect_window(windows, 0, MODE_IN, "2026-11-15", "2026-11-16")
    expect_window(windows, 1, MODE_IN, "2026-11-16", "2026-11-17")


def test_last_week_is_the_monday_to_sunday_before_this_week() -> None:
    expect_window(parse_query_dates("what did I log last week", TODAY), 0, MODE_IN, "2026-11-09", "2026-11-16")
    expect_window(parse_query_dates("last week", "2026-11-18"), 0, MODE_IN, "2026-11-09", "2026-11-16")


def test_last_month_is_the_previous_calendar_month() -> None:
    expect_window(parse_query_dates("last month", TODAY), 0, MODE_IN, "2026-10-01", "2026-11-01")
    expect_window(parse_query_dates("last month", "2026-01-20"), 0, MODE_IN, "2025-12-01", "2026-01-01")


def test_iso_dates_are_one_day() -> None:
    windows = parse_query_dates("the note from 2026-03-04 about it", TODAY)
    assert len(windows) == 1
    expect_window(windows, 0, MODE_IN, "2026-03-04", "2026-03-05")
    assert len(parse_query_dates("ticket 12026-03-04", TODAY)) == 0
    assert len(parse_query_dates("on 2026-02-30", TODAY)) == 0


def test_before_and_after_take_a_month_or_a_day() -> None:
    expect_window(parse_query_dates("what was logged before 2026-03-04", TODAY), 0, MODE_BEFORE, "", "2026-03-04")
    expect_window(parse_query_dates("what came after 2026-03-04", TODAY), 0, MODE_AFTER, "2026-03-05", "")
    expect_window(parse_query_dates("before June", TODAY), 0, MODE_BEFORE, "", "2026-06-01")
    expect_window(parse_query_dates("after June", TODAY), 0, MODE_AFTER, "2026-07-01", "")


def test_event_words_are_not_dates() -> None:
    assert len(parse_query_dates("What was logged right before the Mojo-first plan?", TODAY)) == 0
    assert len(parse_query_dates("After the portal was folded into vinci, what happened?", TODAY)) == 0
    assert len(parse_query_dates("What did the repo use for the may release?", TODAY)) == 0


def test_window_matching_and_boost() -> None:
    windows = parse_query_dates("in June", TODAY)
    assert window_matches(windows[0], "2026-06-30")
    assert not window_matches(windows[0], "2026-07-01")
    assert date_boost(windows, "2026-06-12T10:00:00+00:00") == DATE_BOOST
    assert date_boost(windows, "2026-05-31T23:59:00+00:00") == 0.0
    assert date_boost(windows, "") == 0.0
