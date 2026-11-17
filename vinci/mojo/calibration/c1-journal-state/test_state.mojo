from std.testing import assert_equal

from format import format_span, minutes_to_ticks
from journal import parse_event
from state import State, idea_line, stage_name


# The same lines as fixture.jsonl. Mojo reads \\ as one backslash, so the JSON
# escapes are written doubled here.
def fixture_lines() -> List[String]:
    var lines = List[String]()
    lines.append(
        '{"at":"2026-11-01T09:00:00-06:00","kind":"idea","id":"i1","text":"alpha"}'
    )
    lines.append(
        '{"at":"2026-11-01T09:01:00-06:00","kind":"idea","id":"i2","text":"beta'
        ' \\u2265 gamma"}'
    )
    lines.append(
        '{"at":"2026-11-01T09:02:00-06:00","kind":"idea","id":"i3","text":"delta'
        ' \\"quoted\\""}'
    )
    lines.append(
        '{"at":"2026-11-01T09:03:00-06:00","kind":"idea","id":"i4","text":"eta"}'
    )
    lines.append(
        '{"at":"2026-11-01T09:04:00-06:00","kind":"idea","id":"i5","text":"theta"}'
    )
    lines.append(
        '{"at":"2026-11-01T09:05:00-06:00","kind":"idea","id":"i6","text":"iota"}'
    )
    lines.append(
        '{"at":"2026-11-02T10:00:00-06:00","kind":"stage","id":"i1","text":"Prepare"}'
    )
    lines.append(
        '{"at":"2026-11-02T10:05:00-06:00","kind":"spent","id":"i1","value":45}'
    )
    lines.append(
        '{"at":"2026-11-02T10:06:00-06:00","kind":"stage","id":"i1","text":"Incubate","value":2}'
    )
    lines.append(
        '{"at":"2026-11-03T10:00:00-06:00","kind":"stage","id":"i1","text":"Build"}'
    )
    lines.append(
        '{"at":"2026-11-03T10:10:00-06:00","kind":"spent","id":"i1","value":30}'
    )
    lines.append(
        '{"at":"2026-11-03T11:00:00-06:00","kind":"stage","id":"i2","text":"Build"}'
    )
    lines.append(
        '{"at":"2026-11-03T11:05:00-06:00","kind":"spent","id":"i2","value":59}'
    )
    lines.append(
        '{"at":"2026-11-03T11:10:00-06:00","kind":"ship","id":"i2","text":"v1'
        ' shipped"}'
    )
    lines.append(
        '{"at":"2026-11-03T11:20:00-06:00","kind":"tend","id":"i2","text":"fixed'
        ' the layout"}'
    )
    lines.append(
        '{"at":"2026-11-03T11:30:00-06:00","kind":"spent","id":"i2","value":0.5}'
    )
    lines.append(
        '{"at":"2026-11-03T11:31:00-06:00","kind":"spent","id":"i2","value":0.5}'
    )
    lines.append(
        '{"at":"2026-11-04T09:00:00-06:00","kind":"spent","id":"i3","value":1500}'
    )
    lines.append(
        '{"at":"2026-11-04T09:30:00-06:00","kind":"retire","id":"i3","text":"no'
        ' longer needed"}'
    )
    lines.append(
        '{"at":"2026-11-04T10:00:00-06:00","kind":"spent","id":"i4","value":3000}'
    )
    lines.append(
        '{"at":"2026-11-04T10:01:00-06:00","kind":"stage","id":"i4","text":"Tend"}'
    )
    lines.append(
        '{"at":"2026-11-04T10:02:00-06:00","kind":"tend","id":"i4","text":"kept'
        ' up"}'
    )
    lines.append(
        '{"at":"2026-11-04T10:03:00-06:00","kind":"stage","id":"i5","text":"Incubate","value":3}'
    )
    lines.append(
        '{"at":"2026-11-04T10:04:00-06:00","kind":"spent","id":"i5","value":59.9999}'
    )
    lines.append('{"at":"2026-11-04T10:05:00-06:00","kind":"review","value":3}')
    lines.append(
        '{"at":"2026-11-04T10:06:00-06:00","kind":"bookmark","id":"b1","text":"a'
        ' link",'
        + '"url":"https://example.com/","why":"reference","tags":["x","y"]}'
    )
    return lines^


def build_state(lines: List[String]) raises -> State:
    var state = State()
    for line in lines:
        state.apply(parse_event(line))
    return state^


def test_span() raises:
    assert_equal(format_span(minutes_to_ticks(0.0)), "0m")
    assert_equal(format_span(minutes_to_ticks(59.0)), "59m")
    assert_equal(format_span(minutes_to_ticks(60.0)), "1h0m")
    assert_equal(format_span(minutes_to_ticks(1439.0)), "23h59m")
    assert_equal(format_span(minutes_to_ticks(1440.0)), "1d0h")
    assert_equal(format_span(minutes_to_ticks(1500.0)), "1d1h")
    assert_equal(format_span(minutes_to_ticks(3000.0)), "2d2h")
    # Truncated, not rounded, like (int)TotalMinutes in C#.
    assert_equal(format_span(minutes_to_ticks(59.9999)), "59m")
    print("test_span: ok")


def test_fixture_states() raises:
    var state = build_state(fixture_lines())
    assert_equal(len(state.ideas), 6)

    var expect_ids = List[String]()
    expect_ids.append("i1")
    expect_ids.append("i2")
    expect_ids.append("i3")
    expect_ids.append("i4")
    expect_ids.append("i5")
    expect_ids.append("i6")
    var expect_stages = List[String]()
    expect_stages.append("Build")
    expect_stages.append("Tend")
    expect_stages.append("Retired")
    expect_stages.append("Tend")
    expect_stages.append("Incubate")
    expect_stages.append("Seed")
    var expect_touch = List[String]()
    expect_touch.append("1h15m")
    expect_touch.append("1h0m")
    expect_touch.append("1d1h")
    expect_touch.append("2d2h")
    expect_touch.append("59m")
    expect_touch.append("0m")
    var expect_titles = List[String]()
    expect_titles.append("alpha")
    expect_titles.append("beta ≥ gamma")
    expect_titles.append('delta "quoted"')
    expect_titles.append("eta")
    expect_titles.append("theta")
    expect_titles.append("iota")

    for i in range(6):
        var idea = state.ideas[i].copy()
        assert_equal(idea.id, expect_ids[i])
        assert_equal(stage_name(idea.stage), expect_stages[i])
        assert_equal(format_span(idea.touch), expect_touch[i])
        assert_equal(idea.title, expect_titles[i])
    print("test_fixture_states: ok")


def test_idea_line() raises:
    var state = build_state(fixture_lines())
    assert_equal(idea_line(state.ideas[0]), "i1   Build    touch 1h15m   alpha")
    assert_equal(idea_line(state.ideas[5]), "i6   Seed     touch 0m      iota")
    print("test_idea_line: ok")


def test_string_escapes() raises:
    var event = parse_event(
        '{"kind":"idea","id":"i9","text":"a \\u2265 b \\"q\\" \\\\ \\u00e9'
        ' \\ud83d\\ude00"}'
    )
    assert_equal(event.text, 'a ≥ b "q" \\ é \U0001F600')
    print("test_string_escapes: ok")


def main() raises:
    test_span()
    test_fixture_states()
    test_idea_line()
    test_string_escapes()
    print("test_state: ok")
