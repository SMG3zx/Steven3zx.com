"""Dates in a question, found in code (plan section 3: the model is too weak to parse time
ranges, so no model call is made). Recognised:

  yesterday, today, last week (the Monday-to-Sunday week before this one), last month,
  in|last|during <month> (and "<month> <year>"), an ISO date yyyy-mm-dd, and before|after
  followed by a month or an ISO date.

A window is a half-open range of journal dates, written yyyy-mm-dd. Its mode is "in"
(start <= d < end), "before" (d < end) or "after" (d >= start). A window only BOOSTS a
candidate in the ordering (DATE_BOOST, fusion.py). It never removes one, so a wrong parse
cannot hide the answer.

Relative phrases resolve against the local date of the clock. A month name with no year picks
the most recent one that has begun: "last June" is the one before this month, "in October" may
be this month. Words that are not dates ("before the portal move") are ignored.

Port of mojo/src/dates.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass

from .fusion import DATE_BOOST
from .route import word_tokens
from .timeutil import (
    DAYS_0001_TO_1970,
    TICKS_PER_DAY,
    Instant,
    civil_from_days,
    days_from_civil,
    days_in_month,
    format_instant,
    parse_instant,
    zero_pad,
)

MODE_IN = "in"
MODE_BEFORE = "before"
MODE_AFTER = "after"

MONTH_NAMES = [
    "january", "february", "march", "april", "may", "june", "july",
    "august", "september", "october", "november", "december",
]


@dataclass
class DateWindow:
    """A range of journal dates. start and end are yyyy-mm-dd; end is exclusive."""

    phrase: str
    mode: str
    start: str
    end: str


def iso_of_days(days: int) -> str:
    """yyyy-mm-dd for a count of days since 1970-01-01."""
    civil = civil_from_days(days)
    return zero_pad(civil.year, 4) + "-" + zero_pad(civil.month, 2) + "-" + zero_pad(civil.day, 2)


def is_real_day(iso: str) -> bool:
    """True for a yyyy-mm-dd that names a day of the calendar (no 02-30)."""
    year = int(iso[0:4])
    month = int(iso[5:7])
    day = int(iso[8:10])
    return 1 <= month <= 12 and 1 <= day <= days_in_month(year, month)


def days_of_iso(text: str) -> int:
    """Days since 1970-01-01 for yyyy-mm-dd. Raises on anything else."""
    if len(text) != 10:
        raise ValueError("dates: not yyyy-mm-dd: " + text)
    return days_from_civil(int(text[0:4]), int(text[5:7]), int(text[8:10]))


def month_of_word(word: str) -> int:
    """1 to 12 for a month name or its usual abbreviation, else 0."""
    for i, name in enumerate(MONTH_NAMES):
        if word == name or word == name[:3]:
            return i + 1
    if word == "sept":
        return 9
    return 0


def is_year_word(word: str) -> bool:
    return len(word) == 4 and all("0" <= ch <= "9" for ch in word)


def month_window(phrase: str, year: int, month: int) -> DateWindow:
    """The whole of one calendar month, as an "in" window."""
    start = iso_of_days(days_from_civil(year, month, 1))
    next_year = year
    next_month = month + 1
    if next_month > 12:
        next_month = 1
        next_year += 1
    end = iso_of_days(days_from_civil(next_year, next_month, 1))
    return DateWindow(phrase, MODE_IN, start, end)


def most_recent_year(month: int, today_year: int, today_month: int, strict: bool) -> int:
    """The year of the latest month that has begun. strict leaves out this month, as "last"
    does."""
    if month < today_month or (not strict and month == today_month):
        return today_year
    return today_year - 1


def parse_iso_at(query: str, i: int) -> bool:
    """True when an ISO date starts at byte i of the query (and is not part of a longer
    number)."""
    b = query.encode("utf-8")
    if i + 10 > len(b):
        return False
    if i > 0 and 48 <= b[i - 1] <= 57:
        return False
    if i + 10 < len(b) and 48 <= b[i + 10] <= 57:
        return False
    for k in (0, 1, 2, 3, 5, 6, 8, 9):
        c = b[i + k]
        if c < 48 or c > 57:
            return False
    return b[i + 4] == 45 and b[i + 7] == 45


def preceding_word(query: str, at: int) -> str:
    """The lowercased word that ends just before byte at (spaces skipped)."""
    b = query.encode("utf-8")
    j = at - 1
    while j >= 0 and b[j] == 32:
        j -= 1
    end = j + 1
    while j >= 0:
        c = b[j]
        if not ((65 <= c <= 90) or (97 <= c <= 122)):
            break
        j -= 1
    out = bytearray()
    for k in range(j + 1, end):
        c = b[k]
        if 65 <= c <= 90:
            c += 32
        out.append(c)
    return out.decode("ascii")


def parse_query_dates(query: str, today: str) -> list[DateWindow]:
    """Every date window the question names, against today (yyyy-mm-dd, local)."""
    out: list[DateWindow] = []
    today_days = days_of_iso(today)
    civil = civil_from_days(today_days)
    tokens = word_tokens(query)
    for i, word in enumerate(tokens):
        prev = tokens[i - 1] if i > 0 else ""
        nxt = tokens[i + 1] if i + 1 < len(tokens) else ""
        if word == "yesterday":
            out.append(DateWindow("yesterday", MODE_IN, iso_of_days(today_days - 1), iso_of_days(today_days)))
            continue
        if word == "today":
            out.append(DateWindow("today", MODE_IN, iso_of_days(today_days), iso_of_days(today_days + 1)))
            continue
        if word == "week" and prev == "last":
            # Monday is weekday 0. 1970-01-01 was a Thursday (weekday 3).
            weekday = (today_days + 3) % 7
            monday = today_days - weekday
            out.append(DateWindow("last week", MODE_IN, iso_of_days(monday - 7), iso_of_days(monday)))
            continue
        if word == "month" and prev == "last":
            first_this = days_from_civil(civil.year, civil.month, 1)
            prev_civil = civil_from_days(first_this - 1)
            out.append(month_window("last month", prev_civil.year, prev_civil.month))
            continue
        month = month_of_word(word)
        if month == 0:
            continue
        year_given = is_year_word(nxt)
        year = most_recent_year(month, civil.year, civil.month, False)
        if year_given:
            year = int(nxt)
        if prev in ("before", "after"):
            resolved = month_window(word, year, month)
            if prev == "before":
                out.append(DateWindow(prev + " " + word, MODE_BEFORE, "", resolved.start))
            else:
                out.append(DateWindow(prev + " " + word, MODE_AFTER, resolved.end, ""))
            continue
        if prev == "last":
            year = most_recent_year(month, civil.year, civil.month, True)
            if year_given:
                year = int(nxt)
            out.append(month_window("last " + word, year, month))
            continue
        if prev in ("in", "during") or year_given:
            phrase = prev + " " + word
            if year_given:
                phrase = word + " " + nxt
            out.append(month_window(phrase, year, month))

    # ISO dates, found in the raw text so that the hyphens survive.
    raw = query.encode("utf-8")
    i = 0
    while i + 10 <= len(raw):
        if not parse_iso_at(query, i):
            i += 1
            continue
        iso = query[i : i + 10]
        if not is_real_day(iso):
            i += 10
            continue
        days = days_of_iso(iso)
        before_word = preceding_word(query, i)
        if before_word == "before":
            out.append(DateWindow(before_word + " " + iso, MODE_BEFORE, "", iso))
        elif before_word == "after":
            out.append(DateWindow(before_word + " " + iso, MODE_AFTER, iso_of_days(days + 1), ""))
        else:
            out.append(DateWindow(iso, MODE_IN, iso, iso_of_days(days + 1)))
        i += 10
    return out


def window_matches(window: DateWindow, day: str) -> bool:
    """Whether a journal date (yyyy-mm-dd) falls in the window."""
    if window.mode == MODE_BEFORE:
        return day < window.end
    if window.mode == MODE_AFTER:
        return day >= window.start
    return window.start <= day < window.end


def date_boost(windows: list[DateWindow], valid_from: str) -> float:
    """DATE_BOOST when the fact's date is in any window, else 0. Only the day counts."""
    if len(valid_from) < 10:
        return 0.0
    day = valid_from[:10]
    for w in windows:
        if window_matches(w, day):
            return DATE_BOOST
    return 0.0


def today_of(now: Instant) -> str:
    """The local date of an instant, yyyy-mm-dd."""
    return format_instant(now)[:10]




def clock_now() -> Instant:
    """The clock for one command: VINCI_NOW when set (ISO 8601 with an offset), else the wall
    clock with the local offset."""
    import os
    import time

    pinned = os.environ.get("VINCI_NOW")
    if pinned is not None:
        return parse_instant(pinned)
    unix_ticks = time.time_ns() // 100
    offset_minutes = int(time.localtime().tm_gmtoff) // 60
    return Instant(unix_ticks + DAYS_0001_TO_1970 * TICKS_PER_DAY, offset_minutes)
