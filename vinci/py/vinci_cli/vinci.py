#!/usr/bin/env python3
"""
Vinci CLI - The main entry point.

Usage:
    vinci              # opens chat
    vinci help         # show all commands
    vinci idea "title" # create a new idea
    vinci for i1 "who" # set who will use idea i1
    vinci goal i1 "goal" metric at-least 100 # set a goal
    vinci advance i1 3 # advance idea i1 by 3 days
    vinci ship i1 "what shipped" # ship an idea
    vinci show i1      # show full idea details
    vinci notes        # show all notes
    vinci sources      # show all sources
    vinci ideas        # list all ideas
    vinci pipeline     # show the pipeline status
    vinci next         # show next action
    vinci backup       # backup the journal
    vinci verify       # verify the journal
"""

import sys
import os
from pathlib import Path
from typing import List, Optional

# Add parent directory for imports
script_dir = Path(__file__).parent
sys.path.insert(0, str(script_dir.parent))

from vinci_core import (
    Journal, JournalTx, Event,
    State, Idea, Requirement, Note, Source, Issue, Principle,
    Prediction, Commitment, Bookmark, Metric, Watch, MemoryRecord,
    Instant, user_error, stage_name, local_now,
    command_writes_journal, find_command,
)
from vinci_core.text import (
    pad_right, num, span, day, format_duration,
    ascii_tail, truncate_div, decimal_digits, round_half_up, fixed_text
)
from vinci_core.timeutil import (
    DAYS_0001_TO_1970, TICKS_PER_DAY, TICKS_PER_HOUR, TICKS_PER_MINUTE,
    TICKS_PER_SECOND
)
from vinci_core.session import Session
from vinci_core.state import (
    SEED, PREPARE, INCUBATE, BUILD, TEND, RETIRED,
    stage_code, empty_touch_in, new_idea
)

# Constants
BUILD_LIMIT = 3

# Exit codes
EXIT_OK = 0
EXIT_USER_ERROR = 1
EXIT_FAILURE = 3

def usage():
    print("""Vinci - A knowledge partner CLI

Usage:
    vinci              # opens chat
    vinci help         # show all commands
    vinci idea "title" # create a new idea
    vinci for i1 "who" # set who will use idea i1
    vinci goal i1 "goal" metric at-least 100 # set a goal
    vinci advance i1 3 # advance idea i1 by 3 days
    vinci ship i1 "what shipped" # ship an idea
    vinci show i1      # show full idea details
    vinci notes        # show all notes
    vinci sources      # show all sources
    vinci ideas        # list all ideas
    vinci pipeline     # show the pipeline status
    vinci next         # show next action
    vinci backup       # backup the journal
    vinci verify       # verify the journal
""")

def help():
    print("""Vinci Commands

Chat:
    vinci              # open chat

Idea management:
    vinci idea "title"                 # create new idea
    vinci for i1 "who"                 # set who will use idea
    vinci goal i1 "goal" metric lo hi  # set goal with metric
    vinci advance i1 days              # advance idea by days
    vinci ship i1 "what"               # ship an idea
    vinci used i1                      # mark idea as used
    vinci tend i1                      # tend an idea
    vinci retire i1 "why"              # retire an idea
    vinci watches                      # show watches
    vinci home i1 "path"               # set home directory

Show:
    vinci ideas                       # list all ideas
    vinci pipeline                    # show pipeline status
    vinci next                        # show next action
    vinci show i1                     # show full idea
    vinci show_idea i1                # same as show

Knowledge:
    vinci note "text"                 # create a note
    vinci source "citation" url       # create a source
    vinci link n1 r1                  # link notes
    vinci log metric value           # log a metric
    vinci spec metric lo hi           # set metric spec
    vinci issue i1 "text"             # create issue
    vinci diagnose x1 "cause"         # diagnose issue
    vinci principle x1 "text"         # create principle
    vinci predict i1 "text" conf      # create prediction
    vinci result h1 1                 # record prediction result
    vinci commit "text"               # make a commitment
    vinci kept b1                     # keep commitment
    vinci missed b1                   # miss commitment
    vinci review metric value         # record review

Ops:
    vinci backup                      # backup journal
    vinci verify                      # verify journal
    vinci memory "text"               # memory record

Portal:
    vinci bookmark "title" url        # bookmark URL
    vinci unbookmark i1               # remove bookmark
    vinci serve                       # start portal server
""")

def init_journal(journal_path: str) -> Journal:
    """Initialize or open a journal."""
    if not os.path.exists(journal_path):
        return Journal(journal_path)
    return Journal.open(journal_path)
