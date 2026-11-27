"""Text helpers for command output, plus the decimal digits that num and the JSON writer need.

Column widths and cuts count UTF-16 units, the unit the existing output was laid out in,
and the code below counts the same way where that matters.

Port of mojo/src/text.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass

from .timeutil import (
    DAYS_0001_TO_1970,
    TICKS_PER_DAY,
    TICKS_PER_HOUR,
    TICKS_PER_MINUTE,
    Instant,
    ascii_prefix,
    civil_from_days,
    trimmed_length,
    zero_pad,
)


@dataclass(frozen=True)
class Digits:
    """A decimal value: 0.digits times 10 to the power point. Zero has no digits."""

    negative: bool
    digits: str
    point: int


def utf16_units(cp: int) -> int:
    """How many UTF-16 units a code point takes: 2 for a surrogate pair."""
    return 2 if cp >= 0x10000 else 1


def utf8_code_points(s: str) -> list[int]:
    """The code points of a string. Python strings are always valid, so nothing is refused."""
    return [ord(ch) for ch in s]


def utf16_length(s: str) -> int:
    """The length of the text in UTF-16 units."""
    return sum(utf16_units(cp) for cp in utf8_code_points(s))


def code_points_to_string(cps: list[int]) -> str:
    return "".join(chr(cp) for cp in cps)


def pad_right(s: str, width: int) -> str:
    """Left-aligned: spaces after, never cut. Width counts UTF-16 units."""
    return s + " " * (width - utf16_length(s))


def truncate_div(a: int, b: int) -> int:
    """Integer division rounding toward zero. Python's // floors."""
    q = a // b
    if q < 0 and q * b != a:
        q += 1
    return q


def span(ticks: int) -> str:
    """Text.Span: days and hours, hours and minutes, or whole minutes.

    Each unit is truncated toward zero. Only a span of at least a day or an hour gets the
    bigger form, so a negative span always prints in minutes.
    """
    if ticks >= TICKS_PER_DAY:
        days = ticks // TICKS_PER_DAY
        hours = (ticks // TICKS_PER_HOUR) % 24
        return f"{days}d{hours}h"
    if ticks >= TICKS_PER_HOUR:
        hours = ticks // TICKS_PER_HOUR
        minutes = (ticks // TICKS_PER_MINUTE) % 60
        return f"{hours}h{minutes}m"
    return f"{truncate_div(ticks, TICKS_PER_MINUTE)}m"


def clip(s: str, max_units: int) -> str:
    """At most max UTF-16 units, the last one replaced by an ellipsis.

    The cut is at a UTF-16 index. A cut that falls between the two halves of a surrogate
    pair leaves a lone surrogate, which prints as U+FFFD, so this does the same.
    """
    cps = utf8_code_points(s)
    total = sum(utf16_units(cp) for cp in cps)
    if total <= max_units:
        return s
    if max_units < 1:
        raise ValueError("clip: max must be at least 1")
    keep = max_units - 1
    kept: list[int] = []
    used = 0
    for cp in cps:
        units = utf16_units(cp)
        if used + units <= keep:
            kept.append(cp)
            used += units
        else:
            if used < keep:
                kept.append(0xFFFD)
            break
    kept.append(0x2026)
    return code_points_to_string(kept)


def day(at: Instant) -> str:
    """Text.Day: the calendar date in the instant's own offset, yyyy-MM-dd."""
    days = at.local_ticks() // TICKS_PER_DAY
    date = civil_from_days(days - DAYS_0001_TO_1970)
    return f"{zero_pad(date.year, 4)}-{zero_pad(date.month, 2)}-{zero_pad(date.day, 2)}"


def decimal_digits(v: float) -> Digits:
    """The shortest digits that round-trip to v.

    Python's repr(float) is shortest round-trip, so this reads its digits and exponent back out.
    """
    if v != v or v in (float("inf"), float("-inf")):
        raise ValueError("decimal: value is not finite")
    text = repr(v)
    negative = False
    i = 0
    if text and text[0] == "-":
        negative = True
        i = 1
    all_digits: list[str] = []
    int_count = 0
    seen_point = False
    exp = 0
    while i < len(text):
        c = text[i]
        if "0" <= c <= "9":
            all_digits.append(c)
            if not seen_point:
                int_count += 1
        elif c == ".":
            seen_point = True
        elif c in "eE":
            j = i + 1
            exp_negative = False
            if j < len(text) and text[j] in "+-":
                exp_negative = text[j] == "-"
                j += 1
            if j >= len(text):
                raise ValueError("decimal: bad exponent")
            value = 0
            while j < len(text):
                d = text[j]
                if not ("0" <= d <= "9"):
                    raise ValueError("decimal: bad exponent")
                value = value * 10 + (ord(d) - 48)
                j += 1
            exp = -value if exp_negative else value
            break
        else:
            raise ValueError("decimal: unexpected character")
        i += 1
    point = int_count + exp
    # Drop leading zeros: each one moves the point left by one place.
    start = 0
    while start < len(all_digits) and all_digits[start] == "0":
        start += 1
        point -= 1
    body = "".join(all_digits[start:])
    digits = ascii_prefix(body, trimmed_length(body))
    if len(digits) == 0:
        point = 0
    return Digits(negative, digits, point)


def round_half_up(digits: str, point: int, keep: int) -> Digits:
    """Keep the first keep digits, rounding half up on the next digit.

    keep counts from the first digit, so keep = point + 2 keeps two decimals.
    """
    if keep >= len(digits):
        return Digits(False, digits, point)
    if keep < 0:
        return Digits(False, "", 0)
    round_up = digits[keep] >= "5"
    kept = list(digits[:keep])
    if not round_up:
        body = "".join(kept)
        return Digits(False, ascii_prefix(body, trimmed_length(body)), point)
    # Add one to the last kept digit, carrying leftwards.
    k = len(kept) - 1
    while k >= 0 and kept[k] == "9":
        kept[k] = "0"
        k -= 1
    if k < 0:
        # All nines (or nothing kept): the result is a single 1 one place higher.
        return Digits(False, "1", point + 1)
    kept[k] = chr(ord(kept[k]) + 1)
    body = "".join(kept)
    return Digits(False, ascii_prefix(body, trimmed_length(body)), point)


def fixed_text(d: Digits) -> str:
    """Plain decimal notation with no exponent, from digits and point."""
    out = "-" if d.negative else ""
    if len(d.digits) == 0:
        return out + "0"
    n = len(d.digits)
    if d.point <= 0:
        return out + "0." + "0" * (-d.point) + d.digits
    if d.point >= n:
        return out + d.digits + "0" * (d.point - n)
    return out + d.digits[: d.point] + "." + d.digits[d.point :]


def num(v: float) -> str:
    """A number with at most two decimals, like "0.##" in an invariant culture.

    The double is taken at its 15 most significant digits, rounded half up at two decimals,
    and trailing zeros are dropped. Negative values keep their sign even when they round to
    zero, so -0.001 is "-0".
    """
    if v != v:
        return "NaN"
    if v == float("inf"):
        return "Infinity"
    if v == float("-inf"):
        return "-Infinity"
    d = decimal_digits(v)
    fifteen = round_half_up(d.digits, d.point, 15)
    two = round_half_up(fifteen.digits, fifteen.point, fifteen.point + 2)
    two = Digits(d.negative, two.digits, two.point)
    return fixed_text(two)


def tail(args: list[str], start: int) -> list[str]:
    """The arguments from start on, as a new list. Empty when start is past the end."""
    return list(args[start:])
