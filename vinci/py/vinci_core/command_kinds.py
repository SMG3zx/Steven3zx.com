"""
Command kinds for routing and analysis.
"""

from typing import Dict, List, Optional
from enum import Enum


class CommandKind(str, Enum):
    """Categories of commands."""
    IDEA = "idea"
    KNOWLEDGE = "knowledge"
    OPS = "ops"
    INDEX = "index"
    FEED = "feed"
    PORTAL = "portal"
    
    # Specific commands
    CHAT = "chat"
    SERVE = "serve"
    
    # Idea commands
    IDEA_CREATE = "idea"
    FOR = "for"
    GOAL = "goal"
    REQ = "req"
    CUT = "cut"
    ADVANCE = "advance"
    ADOPT = "adopt"
    HOME = "home"
    WATCH = "watch"
    UNWATCH = "unwatch"
    WATCHES = "watches"
    SPENT = "spent"
    SHIP = "ship"
    USED = "used"
    TEND = "tend"
    RETIRE = "retire"
    IDEAS = "ideas"
    PIPELINE = "pipeline"
    NEXT = "next"
    SHOW = "show"
    
    # Knowledge commands
    NOTE = "note"
    SOURCE = "source"
    LINK = "link"
    LOG = "log"
    SPEC = "spec"
    ISSUE = "issue"
    DIAGNOSE = "diagnose"
    PRINCIPLE = "principle"
    PREDICT = "predict"
    RESULT = "result"
    COMMITMENT = "commit"
    KEPT = "kept"
    MISSED = "missed"
    REVIEW = "review"
    SHOW_IDEA = "show_idea"
    
    # Ops commands
    BACKUP = "backup"
    VERIFY = "verify"
    MEMORY = "memory"
    
    # Index commands
    INDEX_BUILD = "index"
    OPEN = "open"
    RECALL = "recall"
    
    # Feed commands
    FEED_RUN = "feed"
    FEED_ACTIVITY = "activity"
    
    # Portal commands
    PORTAL_BOOKMARK = "bookmark"
    PORTAL_UNBOOKMARK = "unbookmark"


# Map all commands to their categories
COMMAND_TO_KIND: Dict[str, CommandKind] = {
    # Chat and routing
    "chat": CommandKind.CHAT,
    "serve": CommandKind.SERVE,
    
    # Idea commands
    "idea": CommandKind.IDEA_CREATE,
    "for": CommandKind.FOR,
    "goal": CommandKind.GOAL,
    "req": CommandKind.REQ,
    "cut": CommandKind.CUT,
    "advance": CommandKind.ADVANCE,
    "adopt": CommandKind.ADOPT,
    "home": CommandKind.HOME,
    "watch": CommandKind.WATCH,
    "unwatch": CommandKind.UNWATCH,
    "watches": CommandKind.WATCHES,
    "spent": CommandKind.SPENT,
    "ship": CommandKind.SHIP,
    "used": CommandKind.USED,
    "tend": CommandKind.TEND,
    "retire": CommandKind.RETIRE,
    "ideas": CommandKind.IDEAS,
    "pipeline": CommandKind.PIPELINE,
    "next": CommandKind.NEXT,
    "show": CommandKind.SHOW,
    "show_idea": CommandKind.SHOW_IDEA,
    
    # Knowledge commands
    "note": CommandKind.NOTE,
    "source": CommandKind.SOURCE,
    "link": CommandKind.LINK,
    "log": CommandKind.LOG,
    "spec": CommandKind.SPEC,
    "issue": CommandKind.ISSUE,
    "diagnose": CommandKind.DIAGNOSE,
    "principle": CommandKind.PRINCIPLE,
    "predict": CommandKind.PREDICT,
    "result": CommandKind.RESULT,
    "commit": CommandKind.COMMITMENT,
    "kept": CommandKind.KEPT,
    "missed": CommandKind.MISSED,
    "review": CommandKind.REVIEW,
    
    # Ops commands
    "backup": CommandKind.BACKUP,
    "verify": CommandKind.VERIFY,
    "memory": CommandKind.MEMORY,
    
    # Index commands
    "index": CommandKind.INDEX_BUILD,
    "open": CommandKind.OPEN,
    "recall": CommandKind.RECALL,
    
    # Feed commands
    "feed": CommandKind.FEED_RUN,
    
    # Portal commands
    "bookmark": CommandKind.PORTAL_BOOKMARK,
    "unbookmark": CommandKind.PORTAL_UNBOOKMARK,
}


def find_command(cmd: str) -> int:
    """
    Find if a command is recognized.
    
    Returns:
        Index into COMMAND_TO_KIND if found, -1 otherwise.
    """
    return COMMAND_TO_KIND.get(cmd, -1)


def command_writes_journal(cmd: str) -> bool:
    """
    Check if a command writes to the journal.
    
    Commands that write to the journal:
    - idea, for, goal, req, cut, advance, adopt, home, watch, unwatch,
      spent, ship, used, tend, retire, note, source, link, log, spec,
      issue, diagnose, principle, predict, result, commit, kept, missed
    """
    writers = {
        "idea", "for", "goal", "req", "cut", "advance", "adopt", "home",
        "watch", "unwatch", "spent", "ship", "used", "tend", "retire",
        "note", "source", "link", "log", "spec", "issue", "diagnose",
        "principle", "predict", "result", "commit", "kept", "missed",
        "bookmark", "unbookmark",
    }
    return cmd in writers


def is_idea_command(cmd: str) -> bool:
    """Check if command is an idea-related command."""
    return cmd in {
        "idea", "for", "goal", "req", "cut", "advance", "adopt", "home",
        "watch", "unwatch", "watches", "spent", "ship", "used", "tend",
        "retire", "ideas", "pipeline", "next", "show"
    }


def is_knowledge_command(cmd: str) -> bool:
    """Check if command is a knowledge-related command."""
    return cmd in {
        "note", "source", "link", "log", "spec", "issue", "diagnose",
        "principle", "predict", "result", "commit", "kept", "missed",
        "review", "show_idea"
    }


def is_ops_command(cmd: str) -> bool:
    """Check if command is an operations command."""
    return cmd in {
        "backup", "verify", "memory",
    }


def is_index_command(cmd: str) -> bool:
    """Check if command is an index command."""
    return cmd in {
        "index", "open", "recall",
    }


def is_feed_command(cmd: str) -> bool:
    """Check if command is a feed command."""
    return cmd in {
        "feed",
    }


def is_portal_command(cmd: str) -> bool:
    """Check if command is a portal command."""
    return cmd in {
        "bookmark", "unbookmark",
    }
