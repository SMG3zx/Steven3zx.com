# Instants and durations as the journal keeps them. An Instant is UTC ticks since
# 0001-01-01 plus the offset it was written in, which is all the journal keeps.
# A duration is a tick count. One tick is 100 ns.

comptime TICKS_PER_SECOND = 10000000
comptime TICKS_PER_MINUTE = 600000000
comptime TICKS_PER_HOUR = 36000000000
comptime TICKS_PER_DAY = 864000000000
# Days from 0001-01-01 to 1970-01-01, the epoch of the civil-date math below.
comptime DAYS_0001_TO_1970 = 719162


@fieldwise_init
struct CivilDate(Copyable, Movable):
    var year: Int
    var month: Int
    var day: Int


@fieldwise_init
struct Instant(Copyable, Movable):
    """A point in time with the UTC offset it was written in (minutes east)."""

    var utc: Int
    var offset: Int

    def local_ticks(self) -> Int:
        """Ticks since 0001-01-01 in the instant's own offset."""
        return self.utc + self.offset * TICKS_PER_MINUTE

    def add_ticks(self, ticks: Int) -> Instant:
        return Instant(self.utc + ticks, self.offset)

    def add_days(self, days: Float64) -> Instant:
        """Add days (a Float64), cut to whole ticks.

        1e-7 days is 86400 ticks and 1.5e-8 days is 14999 ticks, so the product is
        truncated, not rounded.
        """
        return self.add_ticks(Int(days * Float64(TICKS_PER_DAY)))


def days_from_civil(year: Int, month: Int, day: Int) -> Int:
    """Days since 1970-01-01 for a proleptic Gregorian date (Hinnant's algorithm).
    """
    var y = year
    if month <= 2:
        y -= 1
    var era = y // 400
    var yoe = y - era * 400
    var mp = month + 9 if month <= 2 else month - 3
    var doy = (153 * mp + 2) // 5 + day - 1
    var doe = yoe * 365 + yoe // 4 - yoe // 100 + doy
    return era * 146097 + doe - 719468


def civil_from_days(days: Int) -> CivilDate:
    """The proleptic Gregorian date for days since 1970-01-01."""
    var z = days + 719468
    var era = z // 146097
    var doe = z - era * 146097
    var yoe = (doe - doe // 1460 + doe // 36524 - doe // 146096) // 365
    var y = yoe + era * 400
    var doy = doe - (365 * yoe + yoe // 4 - yoe // 100)
    var mp = (5 * doy + 2) // 153
    var d = doy - (153 * mp + 2) // 5 + 1
    var m = mp + 3 if mp < 10 else mp - 9
    if m <= 2:
        y += 1
    return CivilDate(y, m, d)


def is_leap(year: Int) -> Bool:
    return (year % 4 == 0 and year % 100 != 0) or year % 400 == 0


def days_in_month(year: Int, month: Int) -> Int:
    if month == 2:
        return 29 if is_leap(year) else 28
    if month == 4 or month == 6 or month == 9 or month == 11:
        return 30
    return 31


def zero_pad(value: Int, width: Int) -> String:
    """The value in decimal, left-padded with zeros to the width (sign not handled).
    """
    var digits = String(value)
    var out = String()
    for _ in range(width - digits.byte_length()):
        out += "0"
    return out + digits


def read_digits(b: List[UInt8], at: Int, count: Int) raises -> Int:
    if at < 0 or at + count > len(b):
        raise Error("time: text ended early")
    var v = 0
    for i in range(count):
        var c = Int(b[at + i])
        if c < 48 or c > 57:
            raise Error("time: expected a digit")
        v = v * 10 + (c - 48)
    return v


def expect_byte(b: List[UInt8], at: Int, want: UInt8) raises:
    if at >= len(b) or b[at] != want:
        raise Error("time: unexpected character in timestamp")


def parse_instant(text: String) raises -> Instant:
    """Parse an ISO 8601 timestamp as the journal writes it.

    Accepts yyyy-MM-ddTHH:mm:ss, an optional fraction of 1 to 7 digits, and
    either Z or +hh:mm / -hh:mm. Anything else is refused. The offset is kept.
    """
    var b = List[UInt8]()
    for byte in text.as_bytes():
        b.append(byte)
    var year = read_digits(b, 0, 4)
    expect_byte(b, 4, 45)  # -
    var month = read_digits(b, 5, 2)
    expect_byte(b, 7, 45)  # -
    var day = read_digits(b, 8, 2)
    expect_byte(b, 10, 84)  # T
    var hour = read_digits(b, 11, 2)
    expect_byte(b, 13, 58)  # :
    var minute = read_digits(b, 14, 2)
    expect_byte(b, 16, 58)  # :
    var second = read_digits(b, 17, 2)
    var pos = 19
    var fraction = 0
    if pos < len(b) and b[pos] == 46:  # .
        pos += 1
        var n = 0
        while pos + n < len(b) and b[pos + n] >= 48 and b[pos + n] <= 57:
            n += 1
        if n < 1 or n > 7:
            raise Error("time: a fraction has 1 to 7 digits")
        var scale = 1
        for _ in range(7 - n):
            scale *= 10
        fraction = read_digits(b, pos, n) * scale
        pos += n
    var offset = 0
    if pos < len(b) and b[pos] == 90:  # Z
        pos += 1
    elif pos < len(b) and (b[pos] == 43 or b[pos] == 45):  # + or -
        var sign = -1 if b[pos] == 45 else 1
        var oh = read_digits(b, pos + 1, 2)
        expect_byte(b, pos + 3, 58)  # :
        var om = read_digits(b, pos + 4, 2)
        if oh > 23 or om > 59:
            raise Error("time: offset out of range")
        offset = sign * (oh * 60 + om)
        pos += 6
    else:
        raise Error("time: missing offset")
    if pos != len(b):
        raise Error("time: text after the offset")
    if month < 1 or month > 12 or day < 1 or day > days_in_month(year, month):
        raise Error("time: date out of range")
    if hour > 23 or minute > 59 or second > 59:
        raise Error("time: time out of range")
    var days = days_from_civil(year, month, day) + DAYS_0001_TO_1970
    var local = (
        days * TICKS_PER_DAY
        + (hour * 3600 + minute * 60 + second) * TICKS_PER_SECOND
        + fraction
    )
    return Instant(local - offset * TICKS_PER_MINUTE, offset)


def ascii_prefix(s: String, count: Int) -> String:
    """The first count bytes of an ASCII string."""
    var out = List[UInt8]()
    var src = s.as_bytes()
    for i in range(count):
        out.append(src[i])
    return String(unsafe_from_utf8=out)


def trimmed_length(digits: String) -> Int:
    """The length of an ASCII digit string without its trailing zeros."""
    var src = digits.as_bytes()
    var keep = len(src)
    while keep > 0 and src[keep - 1] == 48:
        keep -= 1
    return keep


def format_instant(at: Instant) -> String:
    """Format an instant as the journal writes it.

    yyyy-MM-ddTHH:mm:ss, then .fffffff with trailing zeros removed (the dot too
    when the fraction is zero), then +hh:mm or -hh:mm. Offset zero is +00:00.
    """
    var local = at.local_ticks()
    var days = local // TICKS_PER_DAY
    var rest = local - days * TICKS_PER_DAY
    var date = civil_from_days(days - DAYS_0001_TO_1970)
    var hour = rest // TICKS_PER_HOUR
    var minute = (rest // TICKS_PER_MINUTE) % 60
    var second = (rest // TICKS_PER_SECOND) % 60
    var fraction = rest % TICKS_PER_SECOND
    var out = (
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
        var digits = zero_pad(fraction, 7)
        out += "." + ascii_prefix(digits, trimmed_length(digits))
    var off = at.offset
    var mag = -off if off < 0 else off
    out += "-" if off < 0 else "+"
    out += zero_pad(mag // 60, 2) + ":" + zero_pad(mag % 60, 2)
    return out


def minutes_to_ticks(minutes: Float64) -> Int:
    """Minutes as ticks: the product is cut to whole ticks toward zero.

    Checked against 2015 values (docs/mojo-gotchas.md): the cut is truncation, not
    round-to-nearest. For example 1440.00000001 gives 864000000005 ticks.
    """
    return Int(minutes * Float64(TICKS_PER_MINUTE))
