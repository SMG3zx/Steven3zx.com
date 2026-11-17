# Query routing, the retrieval plan and the date windows, against a fixed today.
# Today is 2026-11-16, a Monday, so "last week" is 2026-11-09 to 2026-11-16 (exclusive).

from std.testing import assert_equal, assert_false, assert_true

from dates import (
    DateWindow,
    MODE_AFTER,
    MODE_BEFORE,
    MODE_IN,
    date_boost,
    parse_query_dates,
    window_matches,
)
from fusion import DATE_BOOST
from route import plan_of, route_query, word_tokens

comptime TODAY = "2026-11-16"


def expect_window(
    windows: List[DateWindow],
    index: Int,
    mode: String,
    start: String,
    end: String,
) raises:
    assert_equal(windows[index].mode, mode)
    assert_equal(windows[index].start, start)
    assert_equal(windows[index].end, end)


def test_routes_follow_retrieval_cs() raises:
    assert_equal(route_query("When did the portal move?"), "temporal")
    assert_equal(route_query("What was the latest plan?"), "temporal")
    assert_equal(route_query("How do I deploy the site?"), "procedural")
    assert_equal(route_query("Who owns the GoMath repo?"), "graph")
    assert_equal(route_query("Where does the code live?"), "semantic")
    # Whole words only: "steps" does not match "stepson", and "fix" not "fixture".
    assert_equal(route_query("Tell me about my stepson"), "semantic")
    assert_equal(route_query("the fixture set"), "semantic")
    # Temporal wins over procedural, as in RouteQuery.
    assert_equal(route_query("How do I deploy the latest build?"), "temporal")


def test_word_tokens_split_on_anything_but_word_bytes() raises:
    var tokens = word_tokens("Auto-indexed foo_bar, x1!")
    assert_equal(len(tokens), 4)
    assert_equal(tokens[0], "auto")
    assert_equal(tokens[1], "indexed")
    assert_equal(tokens[2], "foo_bar")
    assert_equal(tokens[3], "x1")


def test_plan_multiplies_candidates_by_complexity() raises:
    var simple = plan_of("Where does the code live?", "semantic")
    assert_equal(simple.complexity, "simple")
    assert_equal(simple.candidate_multiplier, 2)
    var temporal = plan_of("When did it change?", "temporal")
    assert_equal(temporal.complexity, "temporal")
    assert_equal(temporal.candidate_multiplier, 4)
    var multi = plan_of(
        "Why do the two services depend on each other?", "semantic"
    )
    assert_equal(multi.complexity, "multi-hop")
    assert_equal(multi.candidate_multiplier, 6)


def test_last_june_is_the_june_before_this_month() raises:
    var windows = parse_query_dates(
        "What was the weather in Lisbon last June?", TODAY
    )
    assert_equal(len(windows), 1)
    expect_window(windows, 0, MODE_IN, "2026-06-01", "2026-07-01")
    assert_equal(windows[0].phrase, "last june")


def test_last_june_in_june_means_last_year() raises:
    var windows = parse_query_dates("last June", "2026-06-15")
    expect_window(windows, 0, MODE_IN, "2025-06-01", "2025-07-01")


def test_in_october_is_this_year_once_october_has_begun() raises:
    var windows = parse_query_dates("What happened in October?", TODAY)
    expect_window(windows, 0, MODE_IN, "2026-10-01", "2026-11-01")
    # Before October has begun, "in October" means last year's.
    var earlier = parse_query_dates("in October", "2026-09-03")
    expect_window(earlier, 0, MODE_IN, "2025-10-01", "2025-11-01")


def test_month_and_year_name_their_year() raises:
    var windows = parse_query_dates("the plan for March 2024", TODAY)
    expect_window(windows, 0, MODE_IN, "2024-03-01", "2024-04-01")
    var december = parse_query_dates("in December 2025", TODAY)
    expect_window(december, 0, MODE_IN, "2025-12-01", "2026-01-01")


def test_yesterday_and_today() raises:
    var windows = parse_query_dates("yesterday and today", TODAY)
    assert_equal(len(windows), 2)
    expect_window(windows, 0, MODE_IN, "2026-11-15", "2026-11-16")
    expect_window(windows, 1, MODE_IN, "2026-11-16", "2026-11-17")


def test_last_week_is_the_monday_to_sunday_before_this_week() raises:
    var windows = parse_query_dates("what did I log last week", TODAY)
    expect_window(windows, 0, MODE_IN, "2026-11-09", "2026-11-16")
    # On a Wednesday, "this week" starts on the Monday before it.
    var wednesday = parse_query_dates("last week", "2026-11-18")
    expect_window(wednesday, 0, MODE_IN, "2026-11-09", "2026-11-16")


def test_last_month_is_the_previous_calendar_month() raises:
    var windows = parse_query_dates("last month", TODAY)
    expect_window(windows, 0, MODE_IN, "2026-10-01", "2026-11-01")
    var january = parse_query_dates("last month", "2026-01-20")
    expect_window(january, 0, MODE_IN, "2025-12-01", "2026-01-01")


def test_iso_dates_are_one_day() raises:
    var windows = parse_query_dates("the note from 2026-03-04 about it", TODAY)
    assert_equal(len(windows), 1)
    expect_window(windows, 0, MODE_IN, "2026-03-04", "2026-03-05")
    # A longer number is not a date.
    assert_equal(len(parse_query_dates("ticket 12026-03-04", TODAY)), 0)
    # An impossible day is not a date.
    assert_equal(len(parse_query_dates("on 2026-02-30", TODAY)), 0)


def test_before_and_after_take_a_month_or_a_day() raises:
    var before_iso = parse_query_dates(
        "what was logged before 2026-03-04", TODAY
    )
    expect_window(before_iso, 0, MODE_BEFORE, "", "2026-03-04")
    var after_iso = parse_query_dates("what came after 2026-03-04", TODAY)
    expect_window(after_iso, 0, MODE_AFTER, "2026-03-05", "")
    var before_month = parse_query_dates("before June", TODAY)
    expect_window(before_month, 0, MODE_BEFORE, "", "2026-06-01")
    var after_month = parse_query_dates("after June", TODAY)
    expect_window(after_month, 0, MODE_AFTER, "2026-07-01", "")


def test_event_words_are_not_dates() raises:
    # "before" and "after" with an event, as in the personal set, name no date.
    assert_equal(
        len(
            parse_query_dates(
                "What was logged right before the Mojo-first plan?", TODAY
            )
        ),
        0,
    )
    assert_equal(
        len(
            parse_query_dates(
                "After the portal was folded into vinci, what happened?", TODAY
            )
        ),
        0,
    )
    assert_equal(
        len(
            parse_query_dates(
                "What did the repo use for the may release?", TODAY
            )
        ),
        0,
    )


def test_window_matching_and_boost() raises:
    var windows = parse_query_dates("in June", TODAY)
    assert_true(window_matches(windows[0], "2026-06-30"))
    assert_false(window_matches(windows[0], "2026-07-01"))
    var boost = date_boost(windows, "2026-06-12T10:00:00+00:00")
    assert_equal(boost, DATE_BOOST)
    assert_equal(date_boost(windows, "2026-05-31T23:59:00+00:00"), 0.0)
    assert_equal(date_boost(windows, ""), 0.0)


def main() raises:
    test_routes_follow_retrieval_cs()
    print("test_routes_follow_retrieval_cs: ok")
    test_word_tokens_split_on_anything_but_word_bytes()
    print("test_word_tokens_split_on_anything_but_word_bytes: ok")
    test_plan_multiplies_candidates_by_complexity()
    print("test_plan_multiplies_candidates_by_complexity: ok")
    test_last_june_is_the_june_before_this_month()
    print("test_last_june_is_the_june_before_this_month: ok")
    test_last_june_in_june_means_last_year()
    print("test_last_june_in_june_means_last_year: ok")
    test_in_october_is_this_year_once_october_has_begun()
    print("test_in_october_is_this_year_once_october_has_begun: ok")
    test_month_and_year_name_their_year()
    print("test_month_and_year_name_their_year: ok")
    test_yesterday_and_today()
    print("test_yesterday_and_today: ok")
    test_last_week_is_the_monday_to_sunday_before_this_week()
    print("test_last_week_is_the_monday_to_sunday_before_this_week: ok")
    test_last_month_is_the_previous_calendar_month()
    print("test_last_month_is_the_previous_calendar_month: ok")
    test_iso_dates_are_one_day()
    print("test_iso_dates_are_one_day: ok")
    test_before_and_after_take_a_month_or_a_day()
    print("test_before_and_after_take_a_month_or_a_day: ok")
    test_event_words_are_not_dates()
    print("test_event_words_are_not_dates: ok")
    test_window_matching_and_boost()
    print("test_window_matching_and_boost: ok")
