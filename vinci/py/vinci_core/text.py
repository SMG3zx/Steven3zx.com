"""
Text utilities for vinci.
"""

import re
from datetime import datetime, timedelta


def ascii_tail(s: str, n: int) -> str:
    """Substring from index n to end."""
    return s[n:] if n >= 0 else s


def truncate_div(a: int, b: int) -> int:
    """Integer division truncating toward zero."""
    return int(a / b)


def num(value: float) -> str:
    """Format a number with decimal digits."""
    if value == int(value):
        return str(int(value))
    return f"{value:.1f}"


def span(ticks: int) -> str:
    """Format ticks as human-readable span."""
    from .timeutil import TICKS_PER_MINUTE, TICKS_PER_HOUR, TICKS_PER_DAY
    
    if ticks < 0:
        return "-"
    
    if ticks >= TICKS_PER_DAY:
        days = ticks // TICKS_PER_DAY
        remainder = ticks % TICKS_PER_DAY
        if remainder == 0:
            return f"{days}d"
        return f"{days}d{remainder // TICKS_PER_HOUR}h"
    
    if ticks >= TICKS_PER_HOUR:
        hours = ticks // TICKS_PER_HOUR
        remainder = ticks % TICKS_PER_HOUR
        if remainder == 0:
            return f"{hours}h"
        return f"{hours}h{remainder // TICKS_PER_MINUTE}m"
    
    if ticks >= TICKS_PER_MINUTE:
        minutes = ticks // TICKS_PER_MINUTE
        remainder = ticks % TICKS_PER_MINUTE
        if remainder == 0:
            return f"{minutes}m"
        return f"{minutes}m{remainder}s"
    
    return f"{ticks}s"


def decimal_digits(value: float) -> tuple:
    """Get decimal digits of a float: (sign, digits, point)."""
    negative = value < 0
    if negative:
        value = -value
    
    # Split into integer and decimal parts
    int_part = int(value)
    dec_part = value - int_part
    
    # Convert to string and split
    s = f"{int_part}.{dec_part:.15f}".rstrip('0')
    if '.' in s:
        int_str, dec_str = s.split('.')
    else:
        int_str, dec_str = s, ""
    
    # Handle zero case
    if int_str == "0" and not dec_str:
        int_str, dec_str = "0", "0"
    
    digits = int_str + dec_str
    point = len(int_str)
    
    return (negative, digits, point)


def round_half_up(value: float, point: int, digits: int) -> float:
    """Round to specified decimal place using round half up."""
    multiplier = 10 ** (digits - point)
    return round(value * multiplier) / multiplier


def fixed_text(value: float) -> str:
    """Format a value with fixed decimal places."""
    if value == int(value):
        return str(int(value))
    return f"{value:.1f}"


def pad_right(s: str, n: int) -> str:
    """Pad string to width n on the right."""
    return s.rjust(n)


def ascii_lower(s: str) -> str:
    """Convert to lowercase (ASCII only)."""
    return s.lower()


def day(instant: float) -> str:
    """Format an instant as a day string."""
    dt = datetime.fromtimestamp(instant)
    return dt.strftime("%Y-%m-%d")


def minutes_to_ticks(minutes: float) -> int:
    """Convert minutes to ticks (100ns units)."""
    from .timeutil import TICKS_PER_MINUTE
    return int(minutes * TICKS_PER_MINUTE)


def add_days(instant: float, days: float) -> float:
    """Add days to an instant timestamp."""
    dt = datetime.fromtimestamp(instant)
    new_dt = dt + timedelta(days=days)
    return new_dt.timestamp()


def parse_instant(text: str) -> tuple:
    """Parse an ISO 8601 instant string."""
    try:
        dt = datetime.fromisoformat(text.replace('Z', '+00:00'))
        return (dt.timestamp(), dt.utcoffset().total_seconds() / 60)
    except ValueError:
        raise ValueError(f"Invalid instant: {text}")


def local_now() -> tuple:
    """Get current local time as instant."""
    import time
    import os
    
    unix_ticks = int(time.time_ns()) // 100
    offset_minutes = int(os.environ.get('TZ', '0') or time.tzname[0])
    
    # Simplified: use wall clock offset
    try:
        gmtoff = os.popen('date +%s%z').read().strip()
        if len(gmtoff) >= 3:
            sign = 1 if gmtoff[0] in '+-' else -1 if gmtoff[0] == '-' else 1
            offset_seconds = int(gmtoff.replace(':', '')) * sign
            offset_minutes = offset_seconds // 60
    except:
        offset_minutes = 0
    
    return (unix_ticks + 3155378975999999999, offset_minutes)  # Approximation


def required_value(value: float) -> float:
    """A field the event must carry."""
    if value is None:
        raise ValueError("event is missing its value")
    return value


def format_duration(ticks: int) -> str:
    """Format a duration as human-readable."""
    from .timeutil import TICKS_PER_DAY, TICKS_PER_HOUR, TICKS_PER_MINUTE
    
    if ticks < 0:
        return "-"
    
    days = ticks // TICKS_PER_DAY
    hours = (ticks % TICKS_PER_DAY) // TICKS_PER_HOUR
    minutes = (ticks % TICKS_PER_HOUR) // TICKS_PER_MINUTE
    seconds = ticks % TICKS_PER_MINUTE
    
    parts = []
    if days > 0:
        parts.append(f"{days}d")
    if hours > 0:
        parts.append(f"{hours}h")
    if minutes > 0:
        parts.append(f"{minutes}m")
    if seconds > 0 and not parts:
        parts.append(f"{seconds}s")
    
    if not parts:
        return "0s"
    
    return "".join(parts)
