"""
Session - The active session with journal transaction and time.
"""

from typing import List, Optional
from .journal import Journal, JournalTx, Event
from .state import State, stage_name
from .timeutil import Instant, local_now
from .text import pad_right


USER_PREFIX = "vinci:"


def user_error(message: str) -> ValueError:
    """Create a user error with the vinci: prefix."""
    return ValueError(USER_PREFIX + message)


class Session:
    """
    Active session with journal transaction and current time.
    
    The session manages a journal transaction and provides access to the
    current state. It tracks touched time and provides helper methods.
    """
    
    def __init__(self, tx: JournalTx, now: Instant):
        self.tx = tx
        self.now = now
        self.state = State()
        self.tracked: List[str] = []
    
    def arg(self, args: List[str], index: int, name: str) -> str:
        """Get argument at index or raise error."""
        if index >= len(args):
            raise user_error(f"missing {name}")
        return args[index]
    
    def record(self, event: Event):
        """Record an event in the transaction."""
        self.tx.append(event)
    
    def commit(self):
        """Commit the transaction."""
        self.tx.commit()
    
    def abort(self):
        """Abort the transaction."""
        self.tx.abort()
    
    def section(self, title: str, lines: List[str]):
        """Output a section with title and lines."""
        print()
        print(f"{title}")
        print("-" * len(title))
        for line in lines:
            print(line)
    
    def next_id(self, prefix: str) -> str:
        """Get next ID with prefix."""
        return self.state.next_id(prefix)
    
    def add_touch(self, ticks: int):
        """Add touch time to current idea."""
        pass  # Would need context about which idea
    
    def notes_about(self, id: str) -> List[int]:
        """Get indices of notes about an idea."""
        return self.state.notes_about(id)
    
    def all_watches(self) -> List:
        """Get all active watches."""
        return self.state.all_watches()
    
    def watched(self, index: int) -> bool:
        """Check if idea is being watched."""
        return self.state.watched(index)


def note_line(note: dict) -> str:
    """Format a note line for display."""
    id_part = pad_right(note.get('id', ''), 4)
    at_part = note.get('at', '')
    text_part = note.get('text', '')
    return f"{id_part} {at_part} {text_part}"


def prediction_line(pred: dict) -> str:
    """Format a prediction line for display."""
    id_part = pad_right(pred.get('id', ''), 4)
    conf = pred.get('confidence', 0)
    text_part = pred.get('text', '')
    return f"{id_part} {conf:.0f}% {text_part}"
