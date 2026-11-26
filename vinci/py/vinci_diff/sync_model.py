"""The types the tracker's differential test is written in: a step and a scenario.

They are kept apart from the runner (sync_diff) and the cases (sync_scenarios), which both
import them, so neither imports the other.
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path

from .fake_activitywatch import Event

Action = Callable[[Path], None]


@dataclass(frozen=True)
class Step:
    """One command. now pins the clock; track False sets VINCI_NO_TRACK=1. mode sets the fake
    server's failure mode before the command. actions change the repositories first, the
    same way on both binaries. {repos} in argv is the repositories' folder."""

    argv: tuple[str, ...]
    now: str
    track: bool = True
    mode: str | None = None
    actions: tuple[Action, ...] = ()


@dataclass(frozen=True)
class Scenario:
    """One case: the fake watcher's data, the repositories, and the steps in order.

    server is "fake" (the in-process fake ActivityWatch) or "closed" (a port nothing listens
    on, so the connection is refused). setup steps run with tracking off, before the steps.
    """

    name: str
    description: str
    steps: tuple[Step, ...]
    journal: tuple[str, ...] = ()
    setup: tuple[Step, ...] = ()
    windows: tuple[Event, ...] = ()
    afk: tuple[Event, ...] = ()
    server: str = "fake"
    repos: Action | None = None
