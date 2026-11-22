# Text helpers for command output, plus the decimal digits that num and the JSON writer
# both need. Strings here are UTF-8, as Mojo keeps them. Column widths and cuts count
# UTF-16 units, the unit the existing output was laid out in, and the code below counts
# the same way where that matters.

from timeutil import (
    Instant,
    TICKS_PER_DAY,
    TICKS_PER_HOUR,
    TICKS_PER_MINUTE,
    DAYS_0001_TO_1970,
    ascii_prefix,
    civil_from_days,
    trimmed_length,
    zero_pad,
)


@fieldwise_init
struct Digits(Copyable, Movable):
    """A decimal value: 0.digits times 10 to the power point. Zero has no digits.
    """

    var negative: Bool
    var digits: String
    var point: Int


def utf8_code_points(s: String) raises -> List[Int]:
    """Decode a UTF-8 string into code points. Mojo strings are valid UTF-8."""
    var out = List[Int]()
    var b = s.as_bytes()
    var i = 0
    while i < len(b):
        var c = Int(b[i])
        if c < 0x80:
            out.append(c)
            i += 1
            continue
        var need: Int
        var cp: Int
        if c >= 0xF0:
            need = 3
            cp = c & 0x07
        elif c >= 0xE0:
            need = 2
            cp = c & 0x0F
        elif c >= 0xC0:
            need = 1
            cp = c & 0x1F
        else:
            raise Error("text: continuation byte without a start")
        if i + need >= len(b):
            raise Error("text: truncated UTF-8")
        for k in range(need):
            cp = (cp << 6) | (Int(b[i + 1 + k]) & 0x3F)
        out.append(cp)
        i += need + 1
    return out^


def utf16_units(cp: Int) -> Int:
    """How many UTF-16 units a code point takes: 2 for a surrogate pair."""
    return 2 if cp >= 0x10000 else 1


def utf16_length(s: String) raises -> Int:
    """The length of the text in UTF-16 units."""
    var total = 0
    for cp in utf8_code_points(s):
        total += utf16_units(cp)
    return total


def utf8_append(mut out: List[UInt8], cp: Int):
    if cp < 0x80:
        out.append(UInt8(cp))
    elif cp < 0x800:
        out.append(UInt8(0xC0 | (cp >> 6)))
        out.append(UInt8(0x80 | (cp & 0x3F)))
    elif cp < 0x10000:
        out.append(UInt8(0xE0 | (cp >> 12)))
        out.append(UInt8(0x80 | ((cp >> 6) & 0x3F)))
        out.append(UInt8(0x80 | (cp & 0x3F)))
    else:
        out.append(UInt8(0xF0 | (cp >> 18)))
        out.append(UInt8(0x80 | ((cp >> 12) & 0x3F)))
        out.append(UInt8(0x80 | ((cp >> 6) & 0x3F)))
        out.append(UInt8(0x80 | (cp & 0x3F)))


def code_points_to_string(cps: List[Int]) -> String:
    var out = List[UInt8]()
    for cp in cps:
        utf8_append(out, cp)
    return String(unsafe_from_utf8=out)


def pad_right(s: String, width: Int) raises -> String:
    """Left-aligned: spaces after, never cut. Width counts UTF-16 units."""
    var out = s
    for _ in range(width - utf16_length(s)):
        out += " "
    return out


def span(ticks: Int) -> String:
    """Text.Span: days and hours, hours and minutes, or whole minutes.

    Each unit is truncated toward zero. Only
    a span of at least a day or an hour gets the bigger form, so a negative
    span always prints in minutes.
    """
    if ticks >= TICKS_PER_DAY:
        var days = ticks // TICKS_PER_DAY
        var hours = (ticks // TICKS_PER_HOUR) % 24
        return String(days) + "d" + String(hours) + "h"
    if ticks >= TICKS_PER_HOUR:
        var hours = ticks // TICKS_PER_HOUR
        var minutes = (ticks // TICKS_PER_MINUTE) % 60
        return String(hours) + "h" + String(minutes) + "m"
    return String(truncate_div(ticks, TICKS_PER_MINUTE)) + "m"


def truncate_div(a: Int, b: Int) -> Int:
    """Integer division rounding toward zero. Mojo's // floors."""
    var q = a // b
    if q < 0 and q * b != a:
        q += 1
    return q


def clip(s: String, max: Int) raises -> String:
    """At most max UTF-16 units, the last one replaced by "…".

    The cut is at a UTF-16 index. A cut that falls between the two halves of a
    surrogate pair leaves a lone surrogate, which prints as U+FFFD, so this does
    the same.
    """
    var cps = utf8_code_points(s)
    var total = 0
    for cp in cps:
        total += utf16_units(cp)
    if total <= max:
        return s
    if max < 1:
        raise Error("clip: max must be at least 1")
    var keep = max - 1
    var kept = List[Int]()
    var used = 0
    for cp in cps:
        var units = utf16_units(cp)
        if used + units <= keep:
            kept.append(cp)
            used += units
        else:
            if used < keep:
                kept.append(0xFFFD)
            break
    kept.append(0x2026)
    return code_points_to_string(kept)


def day(at: Instant) -> String:
    """Text.Day: the calendar date in the instant's own offset, yyyy-MM-dd."""
    var days = at.local_ticks() // TICKS_PER_DAY
    var date = civil_from_days(days - DAYS_0001_TO_1970)
    return (
        zero_pad(date.year, 4)
        + "-"
        + zero_pad(date.month, 2)
        + "-"
        + zero_pad(date.day, 2)
    )


def decimal_digits(v: Float64) raises -> Digits:
    """The shortest digits that round-trip to v.

    Mojo's String(Float64) is shortest round-trip, so this reads its digits and its
    exponent back out.
    """
    if v != v or v > Float64.MAX or v < -Float64.MAX:
        raise Error("decimal: value is not finite")
    var text = String(v)
    var src = text.as_bytes()
    var negative = False
    var i = 0
    if len(src) > 0 and src[0] == 45:  # -
        negative = True
        i = 1
    var all_digits = List[UInt8]()
    var int_count = 0
    var seen_point = False
    var exp = 0
    while i < len(src):
        var c = Int(src[i])
        if c >= 48 and c <= 57:
            all_digits.append(UInt8(c))
            if not seen_point:
                int_count += 1
        elif c == 46:  # .
            seen_point = True
        elif c == 101 or c == 69:  # e E
            var j = i + 1
            var exp_negative = False
            if j < len(src) and (src[j] == 43 or src[j] == 45):
                exp_negative = src[j] == 45
                j += 1
            if j >= len(src):
                raise Error("decimal: bad exponent")
            var value = 0
            while j < len(src):
                var d = Int(src[j])
                if d < 48 or d > 57:
                    raise Error("decimal: bad exponent")
                value = value * 10 + (d - 48)
                j += 1
            exp = -value if exp_negative else value
            break
        else:
            raise Error("decimal: unexpected character")
        i += 1
    var point = int_count + exp
    # Drop leading zeros: each one moves the point left by one place.
    var start = 0
    while start < len(all_digits) and all_digits[start] == 48:
        start += 1
        point -= 1
    var body = List[UInt8]()
    for k in range(start, len(all_digits)):
        body.append(all_digits[k])
    var digits = ascii_prefix(
        String(unsafe_from_utf8=body),
        trimmed_length(String(unsafe_from_utf8=body)),
    )
    if digits.byte_length() == 0:
        point = 0
    return Digits(negative, digits, point)


def round_half_up(digits: String, point: Int, keep: Int) -> Digits:
    """Keep the first keep digits, rounding half up on the next digit.

    keep counts from the first digit, so keep = point + 2 keeps two decimals.
    """
    var src = digits.as_bytes()
    if keep >= len(src):
        return Digits(False, digits, point)
    if keep < 0:
        return Digits(False, "", 0)
    var round_up = src[keep] >= 53  # '5'
    var kept = List[UInt8]()
    for i in range(keep):
        kept.append(src[i])
    if not round_up:
        return Digits(
            False,
            ascii_prefix(
                String(unsafe_from_utf8=kept),
                trimmed_length(String(unsafe_from_utf8=kept)),
            ),
            point,
        )
    # Add one to the last kept digit, carrying leftwards.
    var k = len(kept) - 1
    while k >= 0 and kept[k] == 57:  # 9
        kept[k] = 48
        k -= 1
    if k < 0:
        # All nines (or nothing kept): the result is a single 1 one place higher.
        return Digits(False, "1", point + 1)
    kept[k] = kept[k] + 1
    return Digits(
        False,
        ascii_prefix(
            String(unsafe_from_utf8=kept),
            trimmed_length(String(unsafe_from_utf8=kept)),
        ),
        point,
    )


def fixed_text(d: Digits) -> String:
    """Plain decimal notation with no exponent, from digits and point."""
    var out = String("-") if d.negative else String()
    if d.digits.byte_length() == 0:
        return out + "0"
    var n = d.digits.byte_length()
    if d.point <= 0:
        out += "0."
        for _ in range(-d.point):
            out += "0"
        return out + d.digits
    if d.point >= n:
        out += d.digits
        for _ in range(d.point - n):
            out += "0"
        return out
    return (
        out
        + ascii_prefix(d.digits, d.point)
        + "."
        + ascii_tail(d.digits, d.point)
    )


def ascii_tail(s: String, start: Int) -> String:
    var src = s.as_bytes()
    var out = List[UInt8]()
    for i in range(start, len(src)):
        out.append(src[i])
    return String(unsafe_from_utf8=out)


def num(v: Float64) raises -> String:
    """A number with at most two decimals, like "0.##" in an invariant culture.

    The double is taken at its 15 most significant digits, rounded half up at two
    decimals, and trailing zeros are dropped. Negative values keep their
    sign even when they round to zero, so -0.001 is "-0".
    """
    if v != v:
        return "NaN"
    if v > Float64.MAX:
        return "Infinity"
    if v < -Float64.MAX:
        return "-Infinity"
    var d = decimal_digits(v)
    var fifteen = round_half_up(d.digits, d.point, 15)
    var two = round_half_up(fifteen.digits, fifteen.point, fifteen.point + 2)
    two.negative = d.negative
    return fixed_text(two)
