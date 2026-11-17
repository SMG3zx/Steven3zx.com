"""Scenarios: a starting journal and the steps run against it, stored as JSON.

A scenario file looks like this:

    {
      "description": "what it covers",
      "start": {"kind": "empty"} | {"kind": "fixture", "file": "fixtures/x.jsonl"}
               | {"kind": "real"},
      "folders": ["glider", "canal"],
      "steps": [{"now": "2026-11-16T09:00:00+00:00", "argv": ["idea", "X"], "stdin": ""}]
    }

"real" copies ~/.vinci/journal.jsonl at run time, so no personal data is stored in the repo.
"""

import fnmatch
import json
from dataclasses import dataclass
from pathlib import Path
from typing import Literal, Protocol, cast

DIFFTEST_DIR = Path(__file__).resolve().parents[2] / "difftest"
SCENARIO_DIR = DIFFTEST_DIR / "scenarios"
DEFAULT_FOLDERS = ("glider", "canal")


@dataclass(frozen=True)
class Step:
    now: str
    argv: tuple[str, ...]
    stdin: str = ""

    def to_json(self) -> dict[str, object]:
        out: dict[str, object] = {"now": self.now, "argv": list(self.argv)}
        if self.stdin:
            out["stdin"] = self.stdin
        return out


@dataclass(frozen=True)
class Start:
    kind: Literal["empty", "fixture", "real"]
    file: str = ""

    def to_json(self) -> dict[str, object]:
        if self.kind == "fixture":
            return {"kind": self.kind, "file": self.file}
        return {"kind": self.kind}


@dataclass(frozen=True)
class Scenario:
    name: str
    description: str
    start: Start
    folders: tuple[str, ...]
    steps: tuple[Step, ...]

    def to_json(self) -> dict[str, object]:
        return {
            "description": self.description,
            "start": self.start.to_json(),
            "folders": list(self.folders),
            "steps": [s.to_json() for s in self.steps],
        }


class StepSource(Protocol):
    """Yields the next step. Generated scenarios read the oracle's journal to decide."""

    def next_step(self, journal: str) -> Step | None: ...


class FixedSteps:
    def __init__(self, steps: tuple[Step, ...]) -> None:
        self._steps = steps
        self._next = 0

    def next_step(self, journal: str) -> Step | None:
        if self._next >= len(self._steps):
            return None
        step = self._steps[self._next]
        self._next += 1
        return step


def as_object(value: object, where: str) -> dict[str, object]:
    if not isinstance(value, dict):
        raise TypeError(f"{where}: expected a JSON object")
    return cast("dict[str, object]", value)


def as_list(value: object, where: str) -> list[object]:
    if not isinstance(value, list):
        raise TypeError(f"{where}: expected a JSON array")
    return cast("list[object]", value)


def as_text(value: object, where: str) -> str:
    if not isinstance(value, str):
        raise TypeError(f"{where}: expected a string")
    return value


def _parse_start(raw: object, where: str) -> Start:
    obj = as_object(raw, where)
    kind = as_text(obj.get("kind", "empty"), where)
    if kind == "empty":
        return Start("empty")
    if kind == "real":
        return Start("real")
    if kind == "fixture":
        return Start("fixture", as_text(obj.get("file"), where))
    raise ValueError(f"{where}: unknown start kind '{kind}'")


def _parse_step(raw: object, where: str) -> Step:
    obj = as_object(raw, where)
    argv = tuple(as_text(x, where) for x in as_list(obj.get("argv"), where))
    if not argv:
        raise ValueError(f"{where}: argv must not be empty (no argv means the chat)")
    return Step(
        now=as_text(obj.get("now"), where),
        argv=argv,
        stdin=as_text(obj.get("stdin", ""), where),
    )


def load_scenario(path: Path) -> Scenario:
    where = str(path)
    obj = as_object(json.loads(path.read_text(encoding="utf-8")), where)
    folders = obj.get("folders")
    return Scenario(
        name=path.stem,
        description=as_text(obj.get("description", ""), where),
        start=_parse_start(obj.get("start", {"kind": "empty"}), where),
        folders=(
            tuple(as_text(x, where) for x in as_list(folders, where))
            if folders is not None
            else DEFAULT_FOLDERS
        ),
        steps=tuple(_parse_step(x, where) for x in as_list(obj.get("steps"), where)),
    )


def hand_scenarios(directory: Path, glob: str) -> list[Scenario]:
    """Scenario files whose file stem matches the glob, sorted by name."""
    return [
        load_scenario(p)
        for p in sorted(directory.glob("*.json"))
        if fnmatch.fnmatchcase(p.stem, glob)
    ]
