"""Differential test for the tracker: `vinci sync`, and the sync that runs before every command.

For each case in sync_scenarios, the C# oracle and the Mojo binary each run the same steps
on their own VINCI_HOME, with the clock pinned, a fake ActivityWatch server on 127.0.0.1
(or a port nothing listens on), and git repositories built with fixed dates. After every
step the harness compares the exit code, stdout, stderr, the journal's bytes, and the
queries that reached the fake server (its timeperiods and query text, compared as parsed
JSON). The first difference fails the case.

The C# runs with VINCI_NO_TRACK unset here, so its tracker is on. The CLI diff (vinci_diff)
keeps VINCI_NO_TRACK=1; this one is the only harness where the tracker runs.

    uv run python -m vinci_diff.sync_diff --candidate ../build/vinci           # the goldens
    uv run python -m vinci_diff.sync_diff --oracle --candidate ../build/vinci   # the live oracle
    uv run python -m vinci_diff.sync_diff --record --candidate ../build/vinci   # regenerate

The goldens (mojo/difftest/golden/sync/) hold each step's exit code, standard output, standard
error, journal and fake-server queries. The case's folder is written as <ROOT> and a closed
port as <PORT>, so a golden holds no path of this machine.
"""

from __future__ import annotations

import argparse
import difflib
import fnmatch
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Final, cast

from .fake_activitywatch import FakeActivityWatch, closed_url
from .golden import (
    GOLDEN_DIR,
    GoldenError,
    add_mode_arguments,
    identity_warning,
    journal_step,
    journal_text,
    mode_of,
    read_json,
    write_set,
)
from .oracle import ORACLE
from .sync_model import Scenario, Step
from .sync_scenarios import scenarios

PY_DIR: Final = Path(__file__).resolve().parents[1]
DEFAULT_CANDIDATE: Final = PY_DIR.parent / "build" / "vinci"
GOLDEN_SYNC: Final = GOLDEN_DIR / "sync"
LOOPBACK_PORT: Final = re.compile(r"127\.0\.0\.1:\d+")
TIMEOUT_S: Final = 30.0
TIME_ZONE: Final = "America/Chicago"
CHANNELS: Final = ("exit", "stdout", "stderr", "journal", "queries")


@dataclass(frozen=True)
class Outcome:
    """What one step left behind, for one binary."""

    exit_code: int | None
    stdout: str
    stderr: str
    journal: str
    queries: str


@dataclass(frozen=True)
class Divergence:
    """The first difference in a case: the step, the channel, and a diff of its text."""

    step: int
    channel: str
    lines: tuple[str, ...]


def _environment(home: Path, env_file: Path, step: Step, url: str) -> dict[str, str]:
    env = {key: value for key, value in os.environ.items() if not key.startswith("VINCI_")}
    env.update(
        {
            "LANG": "C.UTF-8",
            "LC_ALL": "C.UTF-8",
            "TZ": TIME_ZONE,
            "VINCI_HOME": str(home),
            "VINCI_ENV_FILE": str(env_file),
            "VINCI_NOW": step.now,
            "VINCI_ACTIVITYWATCH": url,
            "VINCI_MOJO_PY": str(PY_DIR),
        }
    )
    if not step.track:
        env["VINCI_NO_TRACK"] = "1"
    return env


def _argv(step: Step, repos: Path) -> list[str]:
    return [part.replace("{repos}", str(repos)) for part in step.argv]


def _queries(requests: list[dict[str, object]]) -> str:
    return "\n".join(json.dumps(request, sort_keys=True) for request in requests)


def _run(
    binary: Path,
    side: Path,
    repos: Path,
    step: Step,
    url: str,
    server: FakeActivityWatch | None,
    before: int,
) -> Outcome:
    home = side / "home"
    env_file = side / "env.env"
    done = subprocess.run(
        [str(binary), *_argv(step, repos)],
        input=b"",
        capture_output=True,
        cwd=side / "cwd",
        env=_environment(home, env_file, step, url),
        timeout=TIMEOUT_S,
        check=False,
    )
    journal = home / "journal.jsonl"
    queries = [] if server is None else server.requests[before:]
    return Outcome(
        exit_code=done.returncode,
        stdout=done.stdout.decode("utf-8", errors="backslashreplace"),
        stderr=done.stderr.decode("utf-8", errors="backslashreplace"),
        journal=journal.read_text(encoding="utf-8") if journal.exists() else "",
        queries=_queries(queries),
    )


def run_side(binary: Path, case: Path, label: str, scenario: Scenario) -> list[Outcome]:
    """Every step of a case, on one binary, from a clean home and fresh repositories.

    The repositories live at one path shared by both binaries (case/repos), since the
    journal records a home's path. They are built again for each binary.
    """
    repos = case / "repos"
    shutil.rmtree(repos, ignore_errors=True)
    repos.mkdir(parents=True)
    if scenario.repos is not None:
        scenario.repos(repos)

    side = case / label
    shutil.rmtree(side, ignore_errors=True)
    (side / "home").mkdir(parents=True)
    (side / "cwd").mkdir()
    (side / "env.env").write_bytes(b"")
    if scenario.journal:
        (side / "home" / "journal.jsonl").write_text(
            "".join(line + "\n" for line in scenario.journal), encoding="utf-8"
        )

    server = FakeActivityWatch(windows=list(scenario.windows), afk=list(scenario.afk))
    url = closed_url() if scenario.server == "closed" else server.start()
    if scenario.server == "closed":
        server_for_queries: FakeActivityWatch | None = None
    else:
        server_for_queries = server
    outcomes: list[Outcome] = []
    try:
        for step in scenario.setup:
            _run(binary, side, repos, step, url, server_for_queries, 0)
        for step in scenario.steps:
            if step.mode is not None:
                server.mode = step.mode
            for act in step.actions:
                act(repos)
            before = len(server.requests)
            outcomes.append(_run(binary, side, repos, step, url, server_for_queries, before))
    finally:
        server.stop()
    return outcomes


def normalise(outcomes: list[Outcome], root: Path) -> list[Outcome]:
    """Writes this run's folder as <ROOT> and any loopback port as <PORT>, so that two runs, or
    a golden and a run on another machine, compare equal when the behaviour does."""

    def clean(text: str) -> str:
        for needle in sorted({str(root), os.path.realpath(root)}, key=len, reverse=True):
            text = text.replace(needle, "<ROOT>")
        return LOOPBACK_PORT.sub("127.0.0.1:<PORT>", text)

    return [
        Outcome(
            exit_code=o.exit_code,
            stdout=clean(o.stdout),
            stderr=clean(o.stderr),
            journal=clean(o.journal),
            queries=clean(o.queries),
        )
        for o in outcomes
    ]


def steps_json(scenario: Scenario) -> list[dict[str, object]]:
    return [
        {"argv": list(s.argv), "now": s.now, "track": s.track, "mode": s.mode}
        for s in scenario.steps
    ]


def golden_outcomes(payload: dict[str, object], where: str) -> list[Outcome]:
    out: list[Outcome] = []
    previous = ""
    for raw in cast("list[dict[str, object]]", payload["outcomes"]):
        journal = journal_text(previous, cast("dict[str, object]", raw["journal"]), where)
        code = raw.get("exit")
        out.append(
            Outcome(
                exit_code=code if isinstance(code, int) else None,
                stdout=str(raw["stdout"]),
                stderr=str(raw["stderr"]),
                journal=journal,
                queries=str(raw["queries"]),
            )
        )
        previous = journal
    return out


def golden_payload(scenario: Scenario, outcomes: list[Outcome]) -> dict[str, object]:
    chain: list[dict[str, object]] = []
    previous = ""
    for o in outcomes:
        chain.append(
            {
                "exit": o.exit_code,
                "stdout": o.stdout,
                "stderr": o.stderr,
                "queries": o.queries,
                "journal": journal_step(previous, o.journal),
            }
        )
        previous = o.journal
    return {
        "name": scenario.name,
        "description": scenario.description,
        "steps": steps_json(scenario),
        "outcomes": chain,
    }


def compare(oracle: list[Outcome], candidate: list[Outcome]) -> Divergence | None:
    """The first step and channel where the two binaries differ, or None."""
    for index, (left, right) in enumerate(zip(oracle, candidate, strict=True)):
        for channel in CHANNELS:
            a = getattr(left, "exit_code" if channel == "exit" else channel)
            b = getattr(right, "exit_code" if channel == "exit" else channel)
            if a == b:
                continue
            lines = list(
                difflib.unified_diff(
                    f"{a}\n".splitlines(keepends=True),
                    f"{b}\n".splitlines(keepends=True),
                    fromfile="oracle",
                    tofile="candidate",
                )
            )
            return Divergence(index, channel, tuple(line.rstrip("\n") for line in lines))
    return None


def check(
    binary_oracle: Path, binary_candidate: Path, scenario: Scenario, case: Path
) -> tuple[bool, str, list[str]]:
    """Runs one case on both binaries. Returns (passed, the summary line, detail lines)."""
    oracle = run_side(binary_oracle, case, "oracle", scenario)
    candidate = run_side(binary_candidate, case, "candidate", scenario)
    return _verdict(scenario, compare(oracle, candidate))


def check_golden(
    binary_candidate: Path, scenario: Scenario, case: Path, golden: dict[str, object]
) -> tuple[bool, str, list[str]]:
    """Runs one case on the candidate and compares it with the recorded oracle outcomes."""
    if golden.get("steps") != steps_json(scenario):
        return (
            False,
            f"FAIL {scenario.name}: the steps changed since recording (run --record)",
            [],
        )
    want = golden_outcomes(golden, scenario.name)
    got = normalise(run_side(binary_candidate, case, "candidate", scenario), case)
    if len(want) != len(got):
        return (
            False,
            f"FAIL {scenario.name}: the golden has {len(want)} steps, not {len(got)}",
            [],
        )
    return _verdict(scenario, compare(want, got))


def _verdict(scenario: Scenario, divergence: Divergence | None) -> tuple[bool, str, list[str]]:
    count = len(scenario.steps)
    if divergence is None:
        return (True, f"PASS {scenario.name}: {count} steps, every channel matches", [])
    detail = [
        f"  step {divergence.step + 1} of {count}, channel {divergence.channel}:",
        *(f"    {line}" for line in divergence.lines),
    ]
    return (
        False,
        f"FAIL {scenario.name}: step {divergence.step + 1}, {divergence.channel}",
        detail,
    )


def record(candidate: Path, oracle: Path, chosen: list[Scenario]) -> int:
    """Runs every case on the oracle, checks the candidate against that, and writes the goldens
    only if every case agrees."""
    root = Path(tempfile.mkdtemp(prefix="vinci-sync-record-"))
    staged = root / "sync"
    try:
        payloads: dict[str, dict[str, object]] = {}
        for scenario in chosen:
            case = root / "cases" / scenario.name
            payloads[scenario.name] = golden_payload(
                scenario, normalise(run_side(oracle, case, "oracle", scenario), case)
            )
        write_set(staged, payloads, "sync", oracle, {"cases": len(payloads)})
        failed = 0
        for scenario in chosen:
            case = root / "check" / scenario.name
            passed, summary, detail = check_golden(
                candidate, scenario, case, read_json(staged / f"{scenario.name}.json")
            )
            print(summary)
            for line in detail:
                print(line)
            failed += 0 if passed else 1
        if failed:
            print(
                f"record refused: {failed} cases disagree; goldens kept at {staged}",
                file=sys.stderr,
            )
            return 1
        if GOLDEN_SYNC.exists():
            shutil.rmtree(GOLDEN_SYNC)
        shutil.move(str(staged), str(GOLDEN_SYNC))
        print(f"wrote {len(payloads)} goldens to {GOLDEN_SYNC}")
        return 0
    finally:
        shutil.rmtree(root, ignore_errors=True)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="python -m vinci_diff.sync_diff",
        description="Compare the tracker: the goldens (default), the live C# oracle (--oracle), "
        "or record the goldens (--record).",
    )
    add_mode_arguments(parser)
    parser.add_argument(
        "--oracle-binary", type=Path, default=ORACLE, help="the C# binary for --oracle and --record"
    )
    parser.add_argument(
        "--candidate", type=Path, default=DEFAULT_CANDIDATE, help="the Mojo binary under test"
    )
    parser.add_argument("--case", default="*", metavar="GLOB", help="case names to run")
    parser.add_argument("--keep", action="store_true", help="keep the work directory")
    parser.add_argument("--list", action="store_true", help="list the cases and exit")
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    """Runs the harness. A golden set that does not check out exits 2 with the reason."""
    try:
        return _main(argv)
    except GoldenError as exc:
        print(f"sync-diff: {exc}", file=sys.stderr)
        return 2


def _main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    mode = mode_of(args)
    args.candidate = args.candidate.resolve()
    args.oracle_binary = args.oracle_binary.resolve()
    chosen = [s for s in scenarios() if fnmatch.fnmatch(s.name, args.case)]
    if args.list:
        for s in chosen:
            print(f"{s.name}: {s.description}")
        return 0
    checks = [("candidate", args.candidate)]
    if mode != "golden":
        checks.append(("oracle", args.oracle_binary))
    for label, binary in checks:
        if not binary.is_file() or not os.access(binary, os.X_OK):
            print(f"sync-diff: {label} is not an executable file: {binary}", file=sys.stderr)
            return 2
    if not chosen:
        print("sync-diff: no cases match", file=sys.stderr)
        return 2
    if mode == "record":
        return record(args.candidate, args.oracle_binary, chosen)
    if mode == "golden":
        return _golden(args, chosen)
    if GOLDEN_SYNC.exists():
        warning = identity_warning(read_json(GOLDEN_SYNC / "_meta.json"), args.oracle_binary)
        if warning:
            print(warning, file=sys.stderr)
    root = Path(tempfile.mkdtemp(prefix="vinci-sync-diff-"))
    failed = 0
    try:
        for scenario in chosen:
            passed, summary, detail = check(
                args.oracle_binary, args.candidate, scenario, root / scenario.name
            )
            print(summary)
            for line in detail:
                print(line)
            if not passed:
                failed += 1
    finally:
        if args.keep:
            print(f"sync-diff: kept {root}")
        else:
            shutil.rmtree(root, ignore_errors=True)
    print(f"sync-diff: {len(chosen) - failed} of {len(chosen)} cases match")
    return 0 if failed == 0 else 1


def _golden(args: argparse.Namespace, chosen: list[Scenario]) -> int:
    if not (GOLDEN_SYNC / "_meta.json").is_file():
        print(f"sync-diff: no golden set at {GOLDEN_SYNC}: run with --record", file=sys.stderr)
        return 2
    root = Path(tempfile.mkdtemp(prefix="vinci-sync-diff-"))
    failed = 0
    try:
        for scenario in chosen:
            payload = read_json(GOLDEN_SYNC / f"{scenario.name}.json")
            passed, summary, detail = check_golden(
                args.candidate, scenario, root / scenario.name, payload
            )
            print(summary)
            for line in detail:
                print(line)
            if not passed:
                failed += 1
    finally:
        shutil.rmtree(root, ignore_errors=True)
    print(f"sync-diff: {len(chosen) - failed} of {len(chosen)} cases match the goldens")
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
