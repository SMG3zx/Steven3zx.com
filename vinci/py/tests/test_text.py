"""Text helpers (span, clip, day, num), instants and civil dates, against .NET 10 formatting."""

import pytest

from vinci_core.text import clip, day, decimal_digits, num, pad_right, span, utf16_length
from vinci_core.timeutil import civil_from_days, days_from_civil, format_instant, minutes_to_ticks, parse_instant


def test_span() -> None:
    assert span(minutes_to_ticks(0.0)) == "0m"
    assert span(minutes_to_ticks(59.0)) == "59m"
    assert span(minutes_to_ticks(60.0)) == "1h0m"
    assert span(minutes_to_ticks(1439.0)) == "23h59m"
    assert span(minutes_to_ticks(1440.0)) == "1d0h"
    assert span(minutes_to_ticks(1500.0)) == "1d1h"
    assert span(minutes_to_ticks(3000.0)) == "2d2h"
    assert span(minutes_to_ticks(25.0 * 60.0 + 7.0)) == "1d1h"
    # Truncated, not rounded, like (int)TotalMinutes in C#.
    assert span(minutes_to_ticks(59.9999)) == "59m"
    # A negative span is under an hour in TotalHours, so it stays in minutes.
    assert span(minutes_to_ticks(-10.0)) == "-10m"
    assert span(minutes_to_ticks(-90.0)) == "-90m"


def test_ticks() -> None:
    assert minutes_to_ticks(0.5) == 300000000
    assert minutes_to_ticks(1e-7) == 60
    assert minutes_to_ticks(0.1) == 60000000
    assert minutes_to_ticks(2.675) == 1605000000
    assert minutes_to_ticks(59.9999) == 35999940000
    assert minutes_to_ticks(1440.00000001) == 864000000005
    assert minutes_to_ticks(-0.5) == -300000000


def test_clip() -> None:
    assert clip("hello world", 6) == "hello…"
    assert clip("short", 6) == "short"
    assert clip("exact", 5) == "exact"
    assert clip("", 0) == ""
    assert clip("hello", 1) == "…"
    # Non-ASCII counts one unit per code point below U+10000.
    assert clip("héllo wörld", 6) == "héllo…"
    # An astral character is two UTF-16 units. A cut through it leaves a lone surrogate in C#,
    # which the console writes as U+FFFD.
    assert clip("a\U0001F600b", 3) == "a�…"
    assert clip("a\U0001F600b", 4) == "a\U0001F600b"
    assert clip("a\U0001F600b", 2) == "a…"
    with pytest.raises(ValueError):
        clip("abc", 0)


def test_day() -> None:
    assert day(parse_instant("2026-11-16T00:30:00-06:00")) == "2026-11-16"
    # The day is read in the instant's own offset, not in UTC.
    assert day(parse_instant("2026-11-16T00:30:00+14:00")) == "2026-11-16"
    assert day(parse_instant("2026-11-15T23:59:59.9999999-06:00")) == "2026-11-15"
    assert day(parse_instant("0001-01-01T00:00:00Z")) == "0001-01-01"


@pytest.mark.parametrize(
    "value, want",
    [
        (0.125, "0.13"), (2.675, "2.68"), (1.005, "1.01"), (0.005, "0.01"), (0.001, "0"),
        (-0.001, "-0"), (-0.0, "-0"), (0.0, "0"), (1e21, "1000000000000000000000"),
        (123.456, "123.46"), (59.9999, "60"), (0.5, "0.5"), (-2.5, "-2.5"),
        (1.0 / 3.0, "0.33"), (100.0, "100"), (1e-7, "0"), (1234567.891, "1234567.89"),
        (0.995, "1"), (0.994999, "0.99"), (1000000000000000.1, "1000000000000000"),
        (0.085, "0.09"), (0.015, "0.02"), (1.115, "1.12"), (64.06, "64.06"),
        (99.995, "100"), (9.995, "10"), (0.045, "0.05"),
        (12345678901234567890.0, "12345678901234600000"), (0.00499999999999999, "0"),
        (0.1 + 0.2, "0.3"),
    ],
)
def test_num(value: float, want: str) -> None:
    """The expected values come from .NET 10: double.ToString("0.##", InvariantCulture)."""
    assert num(value) == want


def test_decimal_digits() -> None:
    d = decimal_digits(0.0001234)
    assert d.digits == "1234"
    assert d.point == -3
    z = decimal_digits(-0.0)
    assert z.negative
    assert z.digits == ""
    big = decimal_digits(100.0)
    assert big.digits == "1"
    assert big.point == 3


def test_pad_right() -> None:
    assert pad_right("ab", 4) == "ab  "
    assert pad_right("abcdef", 4) == "abcdef"
    assert pad_right("é", 3) == "é  "
    assert utf16_length("a\U0001F600") == 3


def test_instant_round_trip() -> None:
    same = [
        "2026-11-15T10:25:31.3748456-06:00",
        "2026-11-17T02:53:53.9943206+00:00",
        "2026-11-15T09:49:01-06:00",
        "2026-11-15T09:49:01.5+05:30",
        "2026-11-15T09:49:01.0000001-03:00",
        "2026-11-15T09:49:01.00005-03:00",
        "2026-11-15T09:49:01.1234567+00:00",
        "2026-11-15T09:49:01.123456+14:00",
        "2026-11-15T09:49:01-01:00",
        "2026-11-15T09:49:01-00:30",
        "0001-01-01T00:00:00+00:00",
        "9999-12-31T23:59:59.9999999+00:00",
    ]
    for text in same:
        assert format_instant(parse_instant(text)) == text
    assert format_instant(parse_instant("2026-11-15T09:49:01Z")) == "2026-11-15T09:49:01+00:00"
    assert format_instant(parse_instant("2026-11-15T09:49:01.0000000-06:00")) == "2026-11-15T09:49:01-06:00"
    assert format_instant(parse_instant("2026-11-15T09:49:01.5000000-06:00")) == "2026-11-15T09:49:01.5-06:00"


@pytest.mark.parametrize(
    "text",
    [
        "2026-11-15T09:49:01",
        "2026-11-15 09:49:01Z",
        "2026-13-01T00:00:00Z",
        "2026-02-29T00:00:00Z",
        "2026-11-15T24:00:00Z",
        "2026-11-15T09:49:01.12345678Z",
        "2026-11-15T09:49:01+6:00",
        "2026-11-15T09:49:01Zjunk",
    ],
)
def test_instant_errors(text: str) -> None:
    with pytest.raises(ValueError):
        parse_instant(text)


def test_leap_day_in_2028() -> None:
    assert format_instant(parse_instant("2028-02-29T00:00:00Z")) == "2028-02-29T00:00:00+00:00"


def test_civil() -> None:
    assert days_from_civil(1970, 1, 1) == 0
    assert days_from_civil(2000, 3, 1) == 11017
    d = civil_from_days(11017 - 1)
    assert (d.year, d.month, d.day) == (2000, 2, 29)
