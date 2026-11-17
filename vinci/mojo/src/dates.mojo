# Dates in a question, found in code (plan section 3: the model is too weak to parse
# time ranges, so no model call is made). Recognised:
#
#   yesterday, today, last week (the Monday-to-Sunday week before this one),
#   last month, in|last|during <month> (and "<month> <year>"), an ISO date
#   yyyy-mm-dd, and before|after followed by a month or an ISO date.
#
# A window is a half-open range of journal dates, written yyyy-mm-dd. Its mode is
# "in" (start <= d < end), "before" (d < end) or "after" (d >= start). A window only
# BOOSTS a candidate in the ordering (DATE_BOOST, fusion.mojo). It never removes one,
# so a wrong parse cannot hide the answer.
#
# Relative phrases resolve against the local date of the clock: VINCI_NOW when set
# (with its offset), else the wall clock. A month name with no year picks the most
# recent one that has begun: "last June" is the one before this month, "in October"
# may be this month. Words that are not dates ("before the portal move") are ignored.

from std.python import Python, PythonObject

from fusion import DATE_BOOST
from route import word_tokens
from timeutil import (
    DAYS_0001_TO_1970,
    TICKS_PER_DAY,
    Instant,
    ascii_prefix,
    civil_from_days,
    days_from_civil,
    days_in_month,
    format_instant,
    parse_instant,
    zero_pad,
)

comptime MODE_IN = "in"
comptime MODE_BEFORE = "before"
comptime MODE_AFTER = "after"


struct DateWindow(Copyable, Movable):
    """A range of journal dates. start and end are yyyy-mm-dd; end is exclusive.
    """

    var phrase: String
    var mode: String
    var start: String
    var end: String

    def __init__(
        out self,
        var phrase: String,
        var mode: String,
        var start: String,
        var end: String,
    ):
        self.phrase = phrase^
        self.mode = mode^
        self.start = start^
        self.end = end^


def iso_of_days(days: Int) -> String:
    """yyyy-mm-dd for a count of days since 1970-01-01."""
    var civil = civil_from_days(days)
    return (
        zero_pad(civil.year, 4)
        + "-"
        + zero_pad(civil.month, 2)
        + "-"
        + zero_pad(civil.day, 2)
    )


def slice_bytes(text: String, start: Int, end: Int) -> String:
    """The bytes from start to end (exclusive) of an ASCII string."""
    var src = text.as_bytes()
    var out = List[UInt8]()
    for k in range(start, end):
        out.append(src[k])
    return String(unsafe_from_utf8=out)


def is_real_day(iso: String) raises -> Bool:
    """True for a yyyy-mm-dd that names a day of the calendar (no 02-30)."""
    var year = Int(slice_bytes(iso, 0, 4))
    var month = Int(slice_bytes(iso, 5, 7))
    var day = Int(slice_bytes(iso, 8, 10))
    return (
        month >= 1
        and month <= 12
        and day >= 1
        and day <= days_in_month(year, month)
    )


def days_of_iso(text: String) raises -> Int:
    """Days since 1970-01-01 for yyyy-mm-dd. Raises on anything else."""
    if text.byte_length() != 10:
        raise Error("dates: not yyyy-mm-dd: " + text)
    return days_from_civil(
        Int(slice_bytes(text, 0, 4)),
        Int(slice_bytes(text, 5, 7)),
        Int(slice_bytes(text, 8, 10)),
    )


def month_of_word(word: String) -> Int:
    """1 to 12 for a month name or its usual abbreviation, else 0."""
    var names = List[String]()
    names.append("january")
    names.append("february")
    names.append("march")
    names.append("april")
    names.append("may")
    names.append("june")
    names.append("july")
    names.append("august")
    names.append("september")
    names.append("october")
    names.append("november")
    names.append("december")
    for i in range(12):
        var name = names[i]
        if word == name or word == ascii_prefix(name, 3):
            return i + 1
    if word == "sept":
        return 9
    return 0


def is_year_word(word: String) -> Bool:
    if word.byte_length() != 4:
        return False
    for b in word.as_bytes():
        if Int(b) < 48 or Int(b) > 57:
            return False
    return True


def month_window(phrase: String, year: Int, month: Int) raises -> DateWindow:
    """The whole of one calendar month, as an "in" window."""
    var start = iso_of_days(days_from_civil(year, month, 1))
    var next_year = year
    var next_month = month + 1
    if next_month > 12:
        next_month = 1
        next_year += 1
    var end = iso_of_days(days_from_civil(next_year, next_month, 1))
    return DateWindow(phrase, MODE_IN, start, end)


def most_recent_year(
    month: Int, today_year: Int, today_month: Int, strict: Bool
) -> Int:
    """The year of the latest month that has begun. strict leaves out this month, as
    "last" does."""
    if month < today_month or (not strict and month == today_month):
        return today_year
    return today_year - 1


def parse_iso_at(query: String, i: Int) raises -> Int:
    """If an ISO date starts at byte i of the query (and is not part of a longer
    number), return 1; else 0. Used by the scanner; the caller reads the date.
    """
    var b = query.as_bytes()
    if i + 10 > len(b):
        return 0
    if i > 0 and Int(b[i - 1]) >= 48 and Int(b[i - 1]) <= 57:
        return 0
    if i + 10 < len(b) and Int(b[i + 10]) >= 48 and Int(b[i + 10]) <= 57:
        return 0
    for k in [0, 1, 2, 3, 5, 6, 8, 9]:
        var c = Int(b[i + k])
        if c < 48 or c > 57:
            return 0
    if Int(b[i + 4]) != 45 or Int(b[i + 7]) != 45:
        return 0
    return 1


def preceding_word(query: String, at: Int) -> String:
    """The lowercased word that ends just before byte at (spaces skipped)."""
    var b = query.as_bytes()
    var j = at - 1
    while j >= 0 and Int(b[j]) == 32:
        j -= 1
    var end = j + 1
    while j >= 0:
        var c = Int(b[j])
        var is_letter = (c >= 65 and c <= 90) or (c >= 97 and c <= 122)
        if not is_letter:
            break
        j -= 1
    var out = List[UInt8]()
    for k in range(j + 1, end):
        var c = Int(b[k])
        if c >= 65 and c <= 90:
            c += 32
        out.append(UInt8(c))
    return String(unsafe_from_utf8=out)


def parse_query_dates(query: String, today: String) raises -> List[DateWindow]:
    """Every date window the question names, against today (yyyy-mm-dd, local).
    """
    var out = List[DateWindow]()
    var today_days = days_of_iso(today)
    var civil = civil_from_days(today_days)
    var tokens = word_tokens(query)
    for i in range(len(tokens)):
        var word = tokens[i]
        var prev = String("")
        if i > 0:
            prev = tokens[i - 1]
        var next = String("")
        if i + 1 < len(tokens):
            next = tokens[i + 1]
        if word == "yesterday":
            out.append(
                DateWindow(
                    "yesterday",
                    MODE_IN,
                    iso_of_days(today_days - 1),
                    iso_of_days(today_days),
                )
            )
            continue
        if word == "today":
            out.append(
                DateWindow(
                    "today",
                    MODE_IN,
                    iso_of_days(today_days),
                    iso_of_days(today_days + 1),
                )
            )
            continue
        if word == "week" and prev == "last":
            # Monday is weekday 0. 1970-01-01 was a Thursday (weekday 3).
            var weekday = (today_days + 3) % 7
            var monday = today_days - weekday
            out.append(
                DateWindow(
                    "last week",
                    MODE_IN,
                    iso_of_days(monday - 7),
                    iso_of_days(monday),
                )
            )
            continue
        if word == "month" and prev == "last":
            var first_this = days_from_civil(civil.year, civil.month, 1)
            var last_month_days = first_this - 1
            var prev_civil = civil_from_days(last_month_days)
            out.append(
                month_window("last month", prev_civil.year, prev_civil.month)
            )
            continue
        var month = month_of_word(word)
        if month == 0:
            continue
        var year_given = is_year_word(next)
        var year = most_recent_year(month, civil.year, civil.month, False)
        if year_given:
            year = Int(String(next))
        if prev == "before" or prev == "after":
            var resolved = month_window(word, year, month)
            if prev == "before":
                out.append(
                    DateWindow(
                        prev + " " + word, MODE_BEFORE, "", resolved.start
                    )
                )
            else:
                out.append(
                    DateWindow(prev + " " + word, MODE_AFTER, resolved.end, "")
                )
            continue
        if prev == "last":
            year = most_recent_year(month, civil.year, civil.month, True)
            if year_given:
                year = Int(String(next))
            out.append(month_window("last " + word, year, month))
            continue
        if prev == "in" or prev == "during" or year_given:
            var phrase = prev + " " + word
            if year_given:
                phrase = word + " " + next
            out.append(month_window(phrase, year, month))
    # ISO dates, found in the raw text so that the hyphens survive.
    var raw = query.as_bytes()
    var i = 0
    while i + 10 <= len(raw):
        if parse_iso_at(query, i) == 0:
            i += 1
            continue
        var iso = slice_bytes(query, i, i + 10)
        if not is_real_day(iso):
            i += 10
            continue
        var days = days_of_iso(iso)
        var before_word = preceding_word(query, i)
        if before_word == "before":
            out.append(
                DateWindow(before_word + " " + iso, MODE_BEFORE, "", iso)
            )
        elif before_word == "after":
            out.append(
                DateWindow(
                    before_word + " " + iso,
                    MODE_AFTER,
                    iso_of_days(days + 1),
                    "",
                )
            )
        else:
            out.append(DateWindow(iso, MODE_IN, iso, iso_of_days(days + 1)))
        i += 10
    return out^


def window_matches(window: DateWindow, day: String) -> Bool:
    """Whether a journal date (yyyy-mm-dd) falls in the window."""
    if window.mode == MODE_BEFORE:
        return day < window.end
    if window.mode == MODE_AFTER:
        return day >= window.start
    return day >= window.start and day < window.end


def date_boost(windows: List[DateWindow], valid_from: String) -> Float64:
    """DATE_BOOST when the fact's date is in any window, else 0. Only the day counts.
    """
    if valid_from.byte_length() < 10:
        return 0.0
    var day = ascii_prefix(valid_from, 10)
    for w in windows:
        if window_matches(w, day):
            return DATE_BOOST
    return 0.0


def clock_now() raises -> Instant:
    """The clock for one command: VINCI_NOW when set (ISO 8601 with an offset), else
    the wall clock with the local offset (as vinci.mojo's local_now)."""
    var os = Python.import_module("os")
    var pinned = os.environ.get(PythonObject("VINCI_NOW"), Python.none())
    if Bool(pinned != Python.none()):
        return parse_instant(String(pinned))
    var time = Python.import_module("time")
    var unix_ticks = Int(String(time.time_ns())) // 100
    var offset_minutes = Int(String(time.localtime().tm_gmtoff)) // 60
    return Instant(
        unix_ticks + DAYS_0001_TO_1970 * TICKS_PER_DAY, offset_minutes
    )


def today_of(now: Instant) raises -> String:
    """The local date of an instant, yyyy-mm-dd."""
    return ascii_prefix(format_instant(now), 10)
