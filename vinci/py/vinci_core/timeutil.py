"""
Time utilities for vinci.
"""

from datetime import datetime, timedelta
import os
import time


# Constants
DAYS_0001_TO_1970 = 3155378975999999998  # Ticks from 0001-01-01 to 1970-01-01
TICKS_PER_MINUTE = 600000000  # 60 seconds * 1 billion (100ns)
TICKS_PER_HOUR = 36000000000  # 60 minutes * TICKS_PER_MINUTE
TICKS_PER_DAY = 864000000000  # 24 hours * TICKS_PER_HOUR
TICKS_PER_SECOND = 1000000000  # 1 second in 100ns


class Instant:
    """
    An instant with UTC ticks and timezone offset in minutes.
    
    This represents a specific point in time with both the UTC value
    and the timezone offset for formatting purposes.
    """
    
    def __init__(self, utc_ticks: int, offset_minutes: int = 0):
        self.utc = utc_ticks
        self.offset_minutes = offset_minutes
    
    @property
    def local_ticks(self) -> int:
        """Get local ticks (UTC + offset)."""
        return self.utc + self.offset_minutes * TICKS_PER_MINUTE
    
    @property
    def utc(self) -> int:
        """UTC ticks."""
        return self._utc
    
    @utc.setter
    def utc(self, value: int):
        self._utc = value
    
    def copy(self) -> 'Instant':
        """Create a copy of this instant."""
        return Instant(self.utc, self.offset_minutes)
    
    def add_days(self, days: float) -> 'Instant':
        """Add days to this instant."""
        new_utc = self.utc + int(days * TICKS_PER_DAY)
        return Instant(new_utc, self.offset_minutes)
    
    def add_minutes(self, minutes: float) -> 'Instant':
        """Add minutes to this instant."""
        new_utc = self.utc + int(minutes * TICKS_PER_MINUTE)
        return Instant(new_utc, self.offset_minutes)
    
    def add_seconds(self, seconds: float) -> 'Instant':
        """Add seconds to this instant."""
        new_utc = self.utc + int(seconds * TICKS_PER_SECOND)
        return Instant(new_utc, self.offset_minutes)
    
    def subtract(self, other: 'Instant') -> int:
        """Subtract another instant, returning ticks difference."""
        return self.utc - other.utc
    
    def __lt__(self, other: 'Instant') -> bool:
        return self.utc < other.utc
    
    def __le__(self, other: 'Instant') -> bool:
        return self.utc <= other.utc
    
    def __gt__(self, other: 'Instant') -> bool:
        return self.utc > other.utc
    
    def __ge__(self, other: 'Instant') -> bool:
        return self.utc >= other.utc
    
    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Instant):
            return False
        return self.utc == other.utc and self.offset_minutes == other.offset_minutes
    
    def __repr__(self) -> str:
        return f"Instant({self.utc}, {self.offset_minutes})"


def parse_instant(text: str) -> Instant:
    """
    Parse an ISO 8601 instant string.
    
    Accepts formats like:
    - 2026-11-26T02:25:23+00:00
    - 2026-11-26T02:25:23Z
    - 2026-11-26T02:25:23-05:00
    
    Returns an Instant with UTC ticks and offset in minutes.
    """
    try:
        # Handle 'Z' suffix
        if text.endswith('Z'):
            text = text[:-1] + '+00:00'
        
        # Parse the datetime
        if '+' in text or '-' in text[10:]:
            # Has timezone
            dt = datetime.fromisoformat(text)
            offset_seconds = dt.utcoffset().total_seconds()
            offset_minutes = int(offset_seconds / 60)
            utc_timestamp = dt.timestamp()
        else:
            # No timezone, assume UTC
            dt = datetime.fromisoformat(text)
            offset_minutes = 0
            utc_timestamp = dt.timestamp()
        
        # Convert to ticks since 0001-01-01
        # Unix timestamp is seconds since 1970-01-01
        unix_seconds = utc_timestamp
        unix_ticks = unix_seconds * TICKS_PER_SECOND
        
        # Add ticks from 0001-01-01 to 1970-01-01
        total_ticks = unix_ticks + DAYS_0001_TO_1970
        
        return Instant(total_ticks, offset_minutes)
    
    except ValueError as e:
        raise ValueError(f"Invalid instant: {text}") from e


def local_now() -> Instant:
    """
    Get the current local time as an Instant.
    
    Uses the system's wall clock with local timezone offset.
    """
    unix_ticks = int(time.time_ns()) // 100
    offset_minutes = _get_local_offset()
    return Instant(unix_ticks + DAYS_0001_TO_1970, offset_minutes)


def _get_local_offset() -> int:
    """Get the local timezone offset in minutes."""
    try:
        # Try to get offset from date command
        import subprocess
        result = subprocess.run(
            ['date', '+%z'],
            capture_output=True,
            text=True,
            timeout=5
        )
        if result.returncode == 0:
            tz_str = result.stdout.strip()
            if len(tz_str) >= 3:
                sign = 1 if tz_str[0] in '+-' else -1 if tz_str[0] == '-' else 1
                offset_seconds = int(tz_str.replace(':', '')) * sign
                return offset_seconds // 60
    except:
        pass
    
    # Fallback: try environment variables
    import os
    tz_env = os.environ.get('TZ', '')
    if tz_env:
        # Common timezone names
        tz_map = {
            'EST': -300, 'EDT': -240,
            'CST': -360, 'CDT': -300,
            'MST': -420, 'MDT': -360,
            'PST': -480, 'PDT': -420,
        }
        if tz_env in tz_map:
            return tz_map[tz_env]
    
    # Final fallback: assume UTC
    return 0


def minutes_to_ticks(minutes: float) -> int:
    """Convert minutes to ticks."""
    return int(minutes * TICKS_PER_MINUTE)


def ticks_to_minutes(ticks: int) -> float:
    """Convert ticks to minutes."""
    return ticks / TICKS_PER_MINUTE


def ticks_to_hours(ticks: int) -> float:
    """Convert ticks to hours."""
    return ticks / TICKS_PER_HOUR


def ticks_to_days(ticks: int) -> float:
    """Convert ticks to days."""
    return ticks / TICKS_PER_DAY


def day(instant: float) -> str:
    """
    Format an instant as a day string.
    
    Args:
        instant: Unix timestamp (seconds)
    
    Returns:
        Date string in YYYY-MM-DD format
    """
    dt = datetime.fromtimestamp(instant)
    return dt.strftime("%Y-%m-%d")


def day_from_instant(instant: Instant) -> str:
    """
    Format an Instant as a day string.
    
    Args:
        instant: Instant object
    
    Returns:
        Date string in YYYY-MM-DD format
    """
    dt = datetime.fromtimestamp(instant.utc / TICKS_PER_SECOND)
    return dt.strftime("%Y-%m-%d")


def span(ticks: int) -> str:
    """
    Format ticks as human-readable span.
    
    Args:
        ticks: Number of ticks (100ns units)
    
    Returns:
        Human-readable string like "4d2h", "3h15m", etc.
    """
    if ticks < 0:
        return "-"
    
    if ticks >= TICKS_PER_DAY:
        days = ticks // TICKS_PER_DAY
        remainder = ticks % TICKS_PER_DAY
        if remainder == 0:
            return f"{days}d"
        hours = remainder // TICKS_PER_HOUR
        return f"{days}d{hours}h"
    
    if ticks >= TICKS_PER_HOUR:
        hours = ticks // TICKS_PER_HOUR
        remainder = ticks % TICKS_PER_HOUR
        if remainder == 0:
            return f"{hours}h"
        minutes = remainder // TICKS_PER_MINUTE
        return f"{hours}h{minutes}m"
    
    if ticks >= TICKS_PER_MINUTE:
        minutes = ticks // TICKS_PER_MINUTE
        remainder = ticks % TICKS_PER_MINUTE
        if remainder == 0:
            return f"{minutes}m"
        return f"{minutes}m{remainder}s"
    
    return f"{ticks/10000000.0:.1f}s"


def format_duration(ticks: int) -> str:
    """
    Format a duration as human-readable.
    
    Args:
        ticks: Number of ticks
    
    Returns:
        Human-readable duration string
    """
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
