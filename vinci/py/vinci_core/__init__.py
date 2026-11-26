"""
Vinci Core - Pure Python implementation

This is the complete Python port of vinci, a knowledge partner CLI.
"""

from .journal import Journal, JournalTx, Event
from .state import State, Idea, Requirement, Note, Source, Issue, Principle, Prediction, Commitment, Bookmark, Metric, Watch, MemoryRecord, Instant, stage_name, stage_code, empty_touch_in, new_idea
from .session import Session, user_error
from .text import (
    ascii_tail, truncate_div, num, span, decimal_digits, round_half_up, 
    fixed_text, pad_right, ascii_lower, day, minutes_to_ticks
)
from .timeutil import (
    DAYS_0001_TO_1970, TICKS_PER_DAY, TICKS_PER_HOUR, TICKS_PER_MINUTE,
    TICKS_PER_SECOND, Instant, parse_instant, local_now
)
from .command_kinds import CommandKind, command_writes_journal, find_command

__version__ = "1.0.0"
__all__ = [
    # Core classes
    'Journal', 'JournalTx', 'Event',
    'State', 'Idea', 'Requirement', 'Note', 'Source', 'Issue', 'Principle', 
    'Prediction', 'Commitment', 'Bookmark', 'Metric', 'Watch', 'MemoryRecord',
    # Types
    'Instant',
    # Utilities
    'user_error',
    'stage_name', 'stage_code', 'empty_touch_in', 'new_idea',
    'ascii_tail', 'truncate_div', 'num', 'span', 'decimal_digits', 
    'round_half_up', 'fixed_text', 'pad_right', 'ascii_lower', 'day', 'minutes_to_ticks',
    'DAYS_0001_TO_1970', 'TICKS_PER_DAY', 'TICKS_PER_HOUR', 'TICKS_PER_MINUTE',
    'TICKS_PER_SECOND',
    # Command routing
    'CommandKind', 'command_writes_journal', 'find_command',
]
