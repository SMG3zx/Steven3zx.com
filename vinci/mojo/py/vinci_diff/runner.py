"""Runs one scenario against the oracle and the candidate, step by step.

Each side gets its own work directory: a VINCI_HOME that starts from the same journal
bytes, a current directory with the scenario's folders, and an empty VINCI_ENV_FILE. The
work directory's path is replaced with <WORK> in every channel before comparing, because
`path`, `adopt` and `home` print or store absolute paths.
"""

import difflib
import json
import os
import shutil
import subprocess
import tempfile
from dataclasses import dataclass, field
from pathlib import Path

from .scenario import DIFFTEST_DIR, Scenario, Start, Step, StepSource

TIMEOUT_SECONDS = 20.0
CHANNELS = ("exit", "stdout", "stderr", "journal")
ORACLE_EXIT_CODES = (0, 1)


@dataclass(frozen=True)
class Outcome:
    exit_code: int | None  # None: the process timed out
    stdout: str
    stderr: str
    journal: str  # normalised: the work directory is <WORK>
    journal_raw: str  # as written, fed back to generated sources


@dataclass(frozen=True)
class Divergence:
    index: int
    step: Step
    channel: str
    other_channels: tuple[str, ...]
    diff_lines: tuple[str, ...]


@dataclass(frozen=True)
class OracleBug:
    index: int
    step: Step
    exit_code: int | None
    stderr_head: tuple[str, ...]


@dataclass
class ScenarioReport:
    name: str
    steps_run: int = 0
    steps_matching: int = 0
    divergence: Divergence | None = None
    oracle_bug: OracleBug | None = None
    work_dir: Path | None = None
    repro: Path | None = None
    steps: list[Step] = field(default_factory=list[Step])

    @property
    def passed(self) -> bool:
        return self.divergence is None and self.oracle_bug is None


def load_start(start: Start) -> bytes:
    if start.kind == "empty":
        return b""
    if start.kind == "fixture":
        path = DIFFTEST_DIR / start.file
        if not path.is_file():
            raise FileNotFoundError(f"fixture journal not found: {path}")
        return path.read_bytes()
    real = Path.home() / ".vinci" / "journal.jsonl"
    if not real.is_file():
        raise FileNotFoundError(f"no real journal to copy at {real}")
    return real.read_bytes()


def _environment(side_root: Path, now: str) -> dict[str, str]:
    # Inherit everything except VINCI_* so an unrelated setting cannot leak in.
    env = {key: value for key, value in os.environ.items() if not key.startswith("VINCI_")}
    env.update(
        {
            "LANG": "C.UTF-8",
            "LC_ALL": "C.UTF-8",
            "VINCI_HOME": str(side_root / "home"),
            "VINCI_NOW": now,
            "VINCI_NO_TRACK": "1",
            "VINCI_ENV_FILE": str(side_root / "env.env"),
        }
    )
    return env


def _scrub(text: str, side_root: Path) -> str:
    # realpath is the same path with symlinks resolved; replace both spellings.
    for needle in sorted({str(side_root), os.path.realpath(side_root)}, key=len, reverse=True):
        text = text.replace(needle, "<WORK>")
    return text


def _as_bytes(value: object) -> bytes:
    return value if isinstance(value, bytes) else b""


def _prepare(side_root: Path, folders: tuple[str, ...], start: bytes) -> None:
    (side_root / "home").mkdir(parents=True)
    for folder in folders:
        (side_root / "cwd" / folder).mkdir(parents=True)
    (side_root / "env.env").write_bytes(b"")
    if start:
        (side_root / "home" / "journal.jsonl").write_bytes(start)


def run_once(binary: Path, side_root: Path, step: Step, timeout: float) -> Outcome:
    argv = [str(binary), *step.argv]
    try:
        done = subprocess.run(
            argv,
            input=step.stdin.encode("utf-8"),
            capture_output=True,
            cwd=side_root / "cwd",
            env=_environment(side_root, step.now),
            timeout=timeout,
            check=False,
        )
        code: int | None = done.returncode
        out, err = done.stdout, done.stderr
    except subprocess.TimeoutExpired as expired:
        code = None
        out, err = _as_bytes(expired.stdout), _as_bytes(expired.stderr)

    journal_file = side_root / "home" / "journal.jsonl"
    raw = journal_file.read_text(encoding="utf-8") if journal_file.exists() else ""
    return Outcome(
        exit_code=code,
        stdout=_scrub(out.decode("utf-8", errors="backslashreplace"), side_root),
        stderr=_scrub(err.decode("utf-8", errors="backslashreplace"), side_root),
        journal=_scrub(raw, side_root),
        journal_raw=raw,
    )


def _value(outcome: Outcome, channel: str) -> str | int | None:
    match channel:
        case "exit":
            return outcome.exit_code
        case "stdout":
            return outcome.stdout
        case "stderr":
            return outcome.stderr
        case _:
            return outcome.journal


def _diff_lines(channel: str, oracle: Outcome, candidate: Outcome) -> tuple[str, ...]:
    if channel == "exit":
        return (f"exit code: oracle {_show_exit(oracle)}, candidate {_show_exit(candidate)}",)
    a = _value(oracle, channel)
    b = _value(candidate, channel)
    a_lines = str(a).splitlines()
    b_lines = str(b).splitlines()
    return tuple(difflib.unified_diff(a_lines, b_lines, "oracle", "candidate", lineterm="", n=2))


def _show_exit(outcome: Outcome) -> str:
    return "timed out" if outcome.exit_code is None else str(outcome.exit_code)


def _compare(index: int, step: Step, oracle: Outcome, candidate: Outcome) -> Divergence | None:
    differing = [ch for ch in CHANNELS if _value(oracle, ch) != _value(candidate, ch)]
    if not differing:
        return None
    first, others = differing[0], tuple(differing[1:])
    return Divergence(index, step, first, others, _diff_lines(first, oracle, candidate))


def run_scenario(
    scenario: Scenario,
    source: StepSource,
    oracle: Path,
    candidate: Path,
    keep: bool,
    timeout: float = TIMEOUT_SECONDS,
) -> ScenarioReport:
    """Runs steps from the source until it runs out, or one side diverges or the oracle fails."""
    report = ScenarioReport(scenario.name)
    start = load_start(scenario.start)
    root = Path(tempfile.mkdtemp(prefix=f"vinci-diff-{scenario.name}-"))
    report.work_dir = root
    sides = {"oracle": root / "oracle", "candidate": root / "candidate"}
    for side_root in sides.values():
        _prepare(side_root, scenario.folders, start)

    journal = start.decode("utf-8")
    index = 0
    while (step := source.next_step(journal)) is not None:
        report.steps.append(step)
        oracle_out = run_once(oracle, sides["oracle"], step, timeout)
        report.steps_run += 1
        if oracle_out.exit_code not in ORACLE_EXIT_CODES:
            report.oracle_bug = OracleBug(
                index, step, oracle_out.exit_code, _head(oracle_out.stderr)
            )
            break
        cand_out = run_once(candidate, sides["candidate"], step, timeout)
        divergence = _compare(index, step, oracle_out, cand_out)
        if divergence is not None:
            report.divergence = divergence
            break
        report.steps_matching += 1
        journal = oracle_out.journal_raw
        index += 1

    if not report.passed:
        report.repro = _write_repro(root, scenario, report.steps)
    elif not keep:
        shutil.rmtree(root, ignore_errors=True)
        report.work_dir = None
    return report


def _head(text: str, lines: int = 6) -> tuple[str, ...]:
    return tuple(text.splitlines()[:lines])


def _write_repro(root: Path, scenario: Scenario, steps: list[Step]) -> Path:
    """The steps up to and including the failing one, as a scenario file to re-run."""
    repro = Scenario(
        name=scenario.name,
        description=f"repro of {scenario.name}: {len(steps)} steps, last one fails",
        start=scenario.start,
        folders=scenario.folders,
        steps=tuple(steps),
    )
    path = root / f"repro-{scenario.name}.json"
    path.write_text(
        json.dumps(repro.to_json(), indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
    )
    return path
