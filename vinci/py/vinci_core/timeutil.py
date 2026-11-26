"""Instants and durations as the journal keeps them.

An Instant is UTC ticks since 0001-01-01 plus the offset it was written in, which is all
the journal keeps. A duration is a tick count. One tick is 100 ns.

Port of mojo/src/timeutil.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import NamedTuple

TICKS_PER_SECOND = 10_000_000
TICKS_PER_MINUTE = 600_000_000
TICKS_PER_HOUR = 36_000_000_000
TICKS_PER_DAY = 864_000_000_000
# Days from 0001-01-01 to 1970-01-01, the epoch of the civil-date math below.
DAYS_0001_TO_1970 = 719162


class CivilDate(NamedTuple):
    year: int
    month: int
    day: int


@dataclass(frozen=True)
class Instant:
    """A point in time with the UTC offset it was written in (minutes east)."""

    utc: int
    offset: int

    def local_ticks(self) -> int:
        """Ticks since 0001-01-01 in the instant's own offset."""
        return self.utc + self.offset * TICKS_PER_MINUTE

    def add_ticks(self, ticks: int) -> Instant:
        return Instant(self.utc + ticks, self.offset)

    def add_days(self, days: float) -> Instant:
        """Add days (a float), cut to whole ticks.

        1e-7 days is 86400 ticks and 1.5e-8 days is 14999 ticks, so the product is
        truncated, not rounded.
        """
        return self.add_ticks(int(days * float(TICKS_PER_DAY)))


def days_from_civil(year: int, month: int, day: int) -> int:
    """Days since 1970-01-01 for a proleptic Gregorian date (Hinnant's algorithm)."""
    y = year
    if month <= 2:
        y -= 1
    era = y // 400
    yoe = y - era * 400
    mp = month + 9 if month <= 2 else month - 3
    doy = (153 * mp + 2) // 5 + day - 1
    doe = yoe * 365 + yoe // 4 - yoe // 100 + doy
    return era * 146097 + doe - 719468


def civil_from_days(days: int) -> CivilDate:
    """The proleptic Gregorian date for days since 1970-01-01."""
    z = days + 719468
    era = z // 146097
    doe = z - era * 146097
    yoe = (doe - doe // 1460 + doe // 36524 - doe // 146096) // 365
    y = yoe + era * 400
    doy = doe - (365 * yoe + yoe // 4 - yoe // 100)
    mp = (5 * doy + 2) // 153
    d = doy - (153 * mp + 2) // 5 + 1
    m = mp + 3 if mp < 10 else mp - 9
    if m <= 2:
        y += 1
    return CivilDate(y, m, d)


def is_leap(year: int) -> bool:
    return (year % 4 == 0 and year % 100 != 0) or year % 400 == 0


def days_in_month(year: int, month: int) -> int:
    if month == 2:
        return 29 if is_leap(year) else 28
    if month in (4, 6, 9, 11):
        return 30
    return 31


def zero_pad(value: int, width: int) -> str:
    """The value in decimal, left-padded with zeros to the width (sign not handled)."""
    digits = str(value)
    return "0" * (width - len(digits)) + digits


def read_digits(b: bytes, at: int, count: int) -> int:
    if at < 0 or at + count > len(b):
        raise ValueError("time: text ended early")
    v = 0
    for i in range(count):
        c = b[at + i]
        if c < 48 or c > 57:
            raise ValueError("time: expected a digit")
        v = v * 10 + (c - 48)
    return v


def expect_byte(b: bytes, at: int, want: int) -> None:
    if at >= len(b) or b[at] != want:
        raise ValueError("time: unexpected character in timestamp")


def parse_instant(text: str) -> Instant:
    """Parse an ISO 8601 timestamp as the journal writes it.

    Accepts yyyy-MM-ddTHH:mm:ss, an optional fraction of 1 to 7 digits, and either Z or
    +hh:mm / -hh:mm. Anything else is refused. The offset is kept.
    """
    b = text.encode("utf-8")
    year = read_digits(b, 0, 4)
    expect_byte(b, 4, 45)  # -
    month = read_digits(b, 5, 2)
    expect_byte(b, 7, 45)  # -
    day = read_digits(b, 8, 2)
    expect_byte(b, 10, 84)  # T
    hour = read_digits(b, 11, 2)
    expect_byte(b, 13, 58)  # :
    minute = read_digits(b, 14, 2)
    expect_byte(b, 16, 58)  # :
    second = read_digits(b, 17, 2)
    pos = 19
    fraction = 0
    if pos < len(b) and b[pos] == 46:  # .
        pos += 1
        n = 0
        while pos + n < len(b) and 48 <= b[pos + n] <= 57:
            n += 1
        if n < 1 or n > 7:
            raise ValueError("time: a fraction has 1 to 7 digits")
        scale = 10 ** (7 - n)
        fraction = read_digits(b, pos, n) * scale
        pos += n
    offset = 0
    if pos < len(b) and b[pos] == 90:  # Z
        pos += 1
    elif pos < len(b) and b[pos] in (43, 45):  # + or -
        sign = -1 if b[pos] == 45 else 1
        oh = read_digits(b, pos + 1, 2)
        expect_byte(b, pos + 3, 58)  # :
        om = read_digits(b, pos + 4, 2)
        if oh > 23 or om > 59:
            raise ValueError("time: offset out of range")
        offset = sign * (oh * 60 + om)
        pos += 6
    else:
        raise ValueError("time: missing offset")
    if pos != len(b):
        raise ValueError("time: text after the offset")
    if month < 1 or month > 12 or day < 1 or day > days_in_month(year, month):
        raise ValueError("time: date out of range")
    if hour > 23 or minute > 59 or second > 59:
        raise ValueError("time: time out of range")
    days = days_from_civil(year, month, day) + DAYS_0001_TO_1970
    local = (
        days * TICKS_PER_DAY
        + (hour * 3600 + minute * 60 + second) * TICKS_PER_SECOND
        + fraction
    )
    return Instant(local - offset * TICKS_PER_MINUTE, offset)


def ascii_prefix(s: str, count: int) -> str:
    """The first count bytes of an ASCII string."""
    return s[:count]


def trimmed_length(digits: str) -> int:
    """The length of an ASCII digit string without its trailing zeros."""
    keep = len(digits)
    while keep > 0 and digits[keep - 1] == "0":
        keep -= 1
    return keep


def format_instant(at: Instant) -> str:
    """Format an instant as the journal writes it.

    yyyy-MM-ddTHH:mm:ss, then .fffffff with trailing zeros removed (the dot too when the
    fraction is zero), then +hh:mm or -hh:mm. Offset zero is +00:00.
    """
    local = at.local_ticks()
    days = local // TICKS_PER_DAY
    rest = local - days * TICKS_PER_DAY
    date = civil_from_days(days - DAYS_0001_TO_1970)
    hour = rest // TICKS_PER_HOUR
    minute = (rest // TICKS_PER_MINUTE) % 60
    second = (rest // TICKS_PER_SECOND) % 60
    fraction = rest % TICKS_PER_SECOND
    out = (
        zero_pad(date.year, 4)
        + "-"
        + zero_pad(date.month, 2)
        + "-"
        + zero_pad(date.day, 2)
        + "T"
        + zero_pad(hour, 2)
        + ":"
        + zero_pad(minute, 2)
        + ":"
        + zero_pad(second, 2)
    )
    if fraction != 0:
        digits = zero_pad(fraction, 7)
        out += "." + ascii_prefix(digits, trimmed_length(digits))
    off = at.offset
    mag = -off if off < 0 else off
    out += "-" if off < 0 else "+"
    out += zero_pad(mag // 60, 2) + ":" + zero_pad(mag % 60, 2)
    return out


def minutes_to_ticks(minutes: float) -> int:
    """Minutes as ticks: the product is cut to whole ticks toward zero.

    Checked against 2015 values (docs/mojo-gotchas.md): the cut is truncation, not
    round-to-nearest. For example 1440.00000001 gives 864000000005 ticks.
    """
    return int(minutes * float(TICKS_PER_MINUTE))
