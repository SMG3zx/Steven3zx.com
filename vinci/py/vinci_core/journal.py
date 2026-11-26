"""
Journal - Append-only JSON Lines storage for vinci facts.

The journal is the single source of truth. Every fact is written once with its time,
to an append-only file. Nothing is edited or deleted.
"""

import json
import threading
import time
from pathlib import Path
from typing import Optional, List, Any
from dataclasses import dataclass, field


@dataclass
class Event:
    """A journal event: the time, kind, id, text, and optional fields."""
    at: float  # Unix timestamp (seconds)
    kind: str
    id: Optional[str] = None
    text: Optional[str] = None
    value: Optional[float] = None
    hi: Optional[float] = None
    refs: Optional[List[str]] = None
    who: Optional[str] = None
    why: Optional[str] = None
    url: Optional[str] = None
    tags: Optional[List[str]] = None
    use: Optional[bool] = None
    big: Optional[bool] = None
    where_: Optional[str] = None


class Journal:
    """
    Append-only JSON Lines journal.
    
    Every fact is written once with its time. Nothing is edited or deleted.
    The journal file is plain text so it can be inspected with jq, grep, etc.
    """
    
    def __init__(self, path: str):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()
    
    def exists(self) -> bool:
        return self.path.exists()
    
    def read_events(self) -> List[Event]:
        """Read all events from the journal in order."""
        events = []
        if not self.path.exists():
            return events
        
        with self._lock:
            try:
                with open(self.path, 'r', encoding='utf-8') as f:
                    for line in f:
                        line = line.strip()
                        if not line:
                            continue
                        try:
                            data = json.loads(line)
                            event = Event(
                                at=float(data['at']),
                                kind=data.get('kind', ''),
                                id=data.get('id'),
                                text=data.get('text'),
                                value=float(data['value']) if data.get('value') is not None else None,
                                hi=float(data['hi']) if data.get('hi') is not None else None,
                                refs=data.get('refs'),
                                who=data.get('who'),
                                why=data.get('why'),
                                url=data.get('url'),
                                tags=data.get('tags'),
                                use=data.get('use'),
                                big=data.get('big'),
                                where_=data.get('where_'),
                            )
                            events.append(event)
                        except json.JSONDecodeError:
                            # Skip corrupted lines
                            continue
            except Exception as e:
                print(f"Error reading journal: {e}")
        
        return events
    
    def append(self, event: Event) -> None:
        """Append an event to the journal."""
        with self._lock:
            # Read existing events
            existing = self.read_events()
            
            # Append new event
            events = existing + [event]
            
            # Write back
            with open(self.path, 'w', encoding='utf-8') as f:
                for e in events:
                    data = {
                        'at': e.at,
                        'kind': e.kind,
                    }
                    if e.id:
                        data['id'] = e.id
                    if e.text:
                        data['text'] = e.text
                    if e.value is not None:
                        data['value'] = e.value
                    if e.hi is not None:
                        data['hi'] = e.hi
                    if e.refs:
                        data['refs'] = e.refs
                    if e.who:
                        data['who'] = e.who
                    if e.why:
                        data['why'] = e.why
                    if e.url:
                        data['url'] = e.url
                    if e.tags:
                        data['tags'] = e.tags
                    if e.use is not None:
                        data['use'] = e.use
                    if e.big is not None:
                        data['big'] = e.big
                    if e.where_:
                        data['where_'] = e.where_
                    
                    f.write(json.dumps(data, ensure_ascii=False) + '\n')
    
    def count(self) -> int:
        """Return the number of events in the journal."""
        return len(self.read_events())
    
    def get_events_since(self, since: float) -> List[Event]:
        """Get events since a given timestamp."""
        events = self.read_events()
        return [e for e in events if e.at > since]


class JournalTx:
    """
    Transaction for journal writes.
    
    Provides a transactional interface where begin() starts a transaction
    and commit() writes all events atomically.
    """
    
    def __init__(self, journal: Journal):
        self.journal = journal
        self.events: List[Event] = []
    
    def begin(self) -> None:
        """Begin a new transaction."""
        self.events = []
    
    def append(self, event: Event) -> None:
        """Add an event to the transaction."""
        self.events.append(event)
    
    def commit(self) -> None:
        """Commit the transaction, writing all events atomically."""
        if not self.events:
            return
        
        # Read existing events
        existing = self.journal.read_events()
        
        # Append new events
        new_events = existing + self.events
        
        # Write back atomically using temp file
        temp_path = self.journal.path.with_suffix('.tmp')
        try:
            with open(temp_path, 'w', encoding='utf-8') as f:
                for e in new_events:
                    data = {
                        'at': e.at,
                        'kind': e.kind,
                    }
                    if e.id:
                        data['id'] = e.id
                    if e.text:
                        data['text'] = e.text
                    if e.value is not None:
                        data['value'] = e.value
                    if e.hi is not None:
                        data['hi'] = e.hi
                    if e.refs:
                        data['refs'] = e.refs
                    if e.who:
                        data['who'] = e.who
                    if e.why:
                        data['why'] = e.why
                    if e.url:
                        data['url'] = e.url
                    if e.tags:
                        data['tags'] = e.tags
                    if e.use is not None:
                        data['use'] = e.use
                    if e.big is not None:
                        data['big'] = e.big
                    if e.where_:
                        data['where_'] = e.where_
                    
                    f.write(json.dumps(data, ensure_ascii=False) + '\n')
            
            # Atomic rename
            temp_path.rename(self.journal.path)
        except Exception:
            # Clean up temp file on failure
            try:
                temp_path.unlink()
            except:
                pass
            raise
    
    def abort(self) -> None:
        """Abort the transaction, discarding all changes."""
        self.events = []


def event_from_dict(data: dict) -> Event:
    """Create an Event from a dictionary."""
    return Event(
        at=float(data['at']),
        kind=data.get('kind', ''),
        id=data.get('id'),
        text=data.get('text'),
        value=float(data['value']) if data.get('value') is not None else None,
        hi=float(data['hi']) if data.get('hi') is not None else None,
        refs=data.get('refs'),
        who=data.get('who'),
        why=data.get('why'),
        url=data.get('url'),
        tags=data.get('tags'),
        use=data.get('use'),
        big=data.get('big'),
        where_=data.get('where_'),
    )
