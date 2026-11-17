from std.testing import assert_equal, assert_true

from text import clip, day, decimal_digits, num, pad_right, span, utf16_length
from timeutil import (
    civil_from_days,
    days_from_civil,
    format_instant,
    minutes_to_ticks,
    parse_instant,
)


def assert_raises_parse(text: String) raises:
    var raised = False
    try:
        _ = parse_instant(text)
    except:
        raised = True
    assert_true(raised, "expected a parse error for " + text)


def test_span() raises:
    assert_equal(span(minutes_to_ticks(0.0)), "0m")
    assert_equal(span(minutes_to_ticks(59.0)), "59m")
    assert_equal(span(minutes_to_ticks(60.0)), "1h0m")
    assert_equal(span(minutes_to_ticks(1439.0)), "23h59m")
    assert_equal(span(minutes_to_ticks(1440.0)), "1d0h")
    assert_equal(span(minutes_to_ticks(1500.0)), "1d1h")
    assert_equal(span(minutes_to_ticks(3000.0)), "2d2h")
    assert_equal(span(minutes_to_ticks(25.0 * 60.0 + 7.0)), "1d1h")
    # Truncated, not rounded, like (int)TotalMinutes in C#.
    assert_equal(span(minutes_to_ticks(59.9999)), "59m")
    # A negative span is under an hour in TotalHours, so it stays in minutes.
    assert_equal(span(minutes_to_ticks(-10.0)), "-10m")
    assert_equal(span(minutes_to_ticks(-90.0)), "-90m")
    print("test_span: ok")


def test_ticks() raises:
    assert_equal(minutes_to_ticks(0.5), 300000000)
    assert_equal(minutes_to_ticks(1e-7), 60)
    assert_equal(minutes_to_ticks(0.1), 60000000)
    assert_equal(minutes_to_ticks(2.675), 1605000000)
    assert_equal(minutes_to_ticks(59.9999), 35999940000)
    assert_equal(minutes_to_ticks(1440.00000001), 864000000005)
    assert_equal(minutes_to_ticks(-0.5), -300000000)
    print("test_ticks: ok")


def test_clip() raises:
    assert_equal(clip("hello world", 6), "hello…")
    assert_equal(clip("short", 6), "short")
    assert_equal(clip("exact", 5), "exact")
    assert_equal(clip("", 0), "")
    assert_equal(clip("hello", 1), "…")
    # Non-ASCII counts one unit per code point below U+10000.
    assert_equal(clip("héllo wörld", 6), "héllo…")
    # An astral character is two UTF-16 units. A cut through it leaves a lone
    # surrogate in C#, which the console writes as U+FFFD.
    assert_equal(clip("a\U0001F600b", 3), "a�…")
    assert_equal(clip("a\U0001F600b", 4), "a\U0001F600b")
    assert_equal(clip("a\U0001F600b", 2), "a…")
    var raised = False
    try:
        _ = clip("abc", 0)
    except:
        raised = True
    assert_true(raised)
    print("test_clip: ok")


def test_day() raises:
    assert_equal(day(parse_instant("2026-11-16T00:30:00-06:00")), "2026-11-16")
    # The day is read in the instant's own offset, not in UTC: 00:30 on the
    # 16th at +14:00 is still the 16th, although it is the 15th in UTC.
    assert_equal(day(parse_instant("2026-11-16T00:30:00+14:00")), "2026-11-16")
    assert_equal(
        day(parse_instant("2026-11-15T23:59:59.9999999-06:00")), "2026-11-15"
    )
    assert_equal(day(parse_instant("0001-01-01T00:00:00Z")), "0001-01-01")
    print("test_day: ok")


def test_num() raises:
    # The expected values come from .NET 10: double.ToString("0.##", InvariantCulture).
    assert_equal(num(0.125), "0.13")
    assert_equal(num(2.675), "2.68")
    assert_equal(num(1.005), "1.01")
    assert_equal(num(0.005), "0.01")
    assert_equal(num(0.001), "0")
    assert_equal(num(-0.001), "-0")
    assert_equal(num(-0.0), "-0")
    assert_equal(num(0.0), "0")
    assert_equal(num(1e21), "1000000000000000000000")
    assert_equal(num(123.456), "123.46")
    assert_equal(num(59.9999), "60")
    assert_equal(num(0.5), "0.5")
    assert_equal(num(-2.5), "-2.5")
    assert_equal(num(1.0 / 3.0), "0.33")
    assert_equal(num(100.0), "100")
    assert_equal(num(1e-7), "0")
    assert_equal(num(1234567.891), "1234567.89")
    assert_equal(num(0.995), "1")
    assert_equal(num(0.994999), "0.99")
    assert_equal(num(1000000000000000.1), "1000000000000000")
    assert_equal(num(0.085), "0.09")
    assert_equal(num(0.015), "0.02")
    assert_equal(num(1.115), "1.12")
    assert_equal(num(64.06), "64.06")
    assert_equal(num(99.995), "100")
    assert_equal(num(9.995), "10")
    assert_equal(num(0.045), "0.05")
    assert_equal(num(12345678901234567890.0), "12345678901234600000")
    assert_equal(num(0.00499999999999999), "0")
    var sum = 0.1 + 0.2
    assert_equal(num(sum), "0.3")
    print("test_num: ok")


def test_decimal_digits() raises:
    var d = decimal_digits(0.0001234)
    assert_equal(d.digits, "1234")
    assert_equal(d.point, -3)
    var z = decimal_digits(-0.0)
    assert_true(z.negative)
    assert_equal(z.digits, "")
    var big = decimal_digits(100.0)
    assert_equal(big.digits, "1")
    assert_equal(big.point, 3)
    print("test_decimal_digits: ok")


def test_pad_right() raises:
    assert_equal(pad_right("ab", 4), "ab  ")
    assert_equal(pad_right("abcdef", 4), "abcdef")
    assert_equal(pad_right("é", 3), "é  ")
    assert_equal(utf16_length("a\U0001F600"), 3)
    print("test_pad_right: ok")


def test_instant_round_trip() raises:
    # The shapes in the real journal, and the probe's samples through System.Text.Json.
    var same = List[String]()
    same.append("2026-11-15T10:25:31.3748456-06:00")
    same.append("2026-11-17T02:53:53.9943206+00:00")
    same.append("2026-11-15T09:49:01-06:00")
    same.append("2026-11-15T09:49:01.5+05:30")
    same.append("2026-11-15T09:49:01.0000001-03:00")
    same.append("2026-11-15T09:49:01.00005-03:00")
    same.append("2026-11-15T09:49:01.1234567+00:00")
    same.append("2026-11-15T09:49:01.123456+14:00")
    same.append("2026-11-15T09:49:01-01:00")
    same.append("2026-11-15T09:49:01-00:30")
    same.append("0001-01-01T00:00:00+00:00")
    same.append("9999-12-31T23:59:59.9999999+00:00")
    for text in same:
        assert_equal(format_instant(parse_instant(text)), text)
    # Z is read as offset zero, and written as +00:00.
    assert_equal(
        format_instant(parse_instant("2026-11-15T09:49:01Z")),
        "2026-11-15T09:49:01+00:00",
    )
    # A fraction of zero is dropped, and so is the dot.
    assert_equal(
        format_instant(parse_instant("2026-11-15T09:49:01.0000000-06:00")),
        "2026-11-15T09:49:01-06:00",
    )
    assert_equal(
        format_instant(parse_instant("2026-11-15T09:49:01.5000000-06:00")),
        "2026-11-15T09:49:01.5-06:00",
    )
    print("test_instant_round_trip: ok")


def test_instant_errors() raises:
    assert_raises_parse("2026-11-15T09:49:01")
    assert_raises_parse("2026-11-15 09:49:01Z")
    assert_raises_parse("2026-13-01T00:00:00Z")
    assert_raises_parse("2026-02-29T00:00:00Z")
    assert_raises_parse("2026-11-15T24:00:00Z")
    assert_raises_parse("2026-11-15T09:49:01.12345678Z")
    assert_raises_parse("2026-11-15T09:49:01+6:00")
    assert_raises_parse("2026-11-15T09:49:01Zjunk")
    # 2028 is a leap year, so 29 February is fine there.
    assert_equal(
        format_instant(parse_instant("2028-02-29T00:00:00Z")),
        "2028-02-29T00:00:00+00:00",
    )
    print("test_instant_errors: ok")


def test_civil() raises:
    assert_equal(days_from_civil(1970, 1, 1), 0)
    assert_equal(days_from_civil(2000, 3, 1), 11017)
    var d = civil_from_days(11017 - 1)
    assert_equal(d.year, 2000)
    assert_equal(d.month, 2)
    assert_equal(d.day, 29)
    print("test_civil: ok")


def main() raises:
    test_span()
    test_ticks()
    test_clip()
    test_day()
    test_num()
    test_decimal_digits()
    test_pad_right()
    test_instant_round_trip()
    test_instant_errors()
    test_civil()
    print("test_text: ok")
