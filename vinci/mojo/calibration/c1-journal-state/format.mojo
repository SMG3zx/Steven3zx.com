# Formatting for `vinci ideas`: C# TimeSpan arithmetic and the Span text.

# .NET ticks: 100 ns each.
comptime TICKS_PER_MINUTE = 600000000
comptime TICKS_PER_HOUR = 36000000000
comptime TICKS_PER_DAY = 864000000000


def minutes_to_ticks(minutes: Float64) -> Int:
    # TimeSpan.FromMinutes rounds to the nearest tick.
    return Int(minutes * 600000000.0 + 0.5)


def format_span(ticks: Int) -> String:
    # Mirrors Text.Span in src/App.cs: days and hours, hours and minutes,
    # or whole minutes. Each unit is truncated, never rounded.
    if ticks >= TICKS_PER_DAY:
        var days = ticks // TICKS_PER_DAY
        var hours = (ticks // TICKS_PER_HOUR) % 24
        return String(days) + "d" + String(hours) + "h"
    if ticks >= TICKS_PER_HOUR:
        var hours = ticks // TICKS_PER_HOUR
        var minutes = (ticks // TICKS_PER_MINUTE) % 60
        return String(hours) + "h" + String(minutes) + "m"
    return String(ticks // TICKS_PER_MINUTE) + "m"


def pad_right(s: String, width: Int) -> String:
    # Left-aligned like C#'s {x,-n}: pad with spaces, never truncate.
    var out = s
    while out.byte_length() < width:
        out += " "
    return out
