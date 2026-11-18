"""Command line: pick scenarios, run them in parallel, print the report, set the exit code.

    uv run python -m vinci_diff --candidate ../build/vinci                 # against the goldens
    uv run python -m vinci_diff --oracle --candidate ../build/vinci         # against the live oracle
    uv run python -m vinci_diff --record --candidate ../build/vinci         # regenerate the goldens

The default needs no .NET and no oracle directory: it replays the recorded steps against the
candidate and compares each step with the recorded outcome. --record runs the oracle, checks
the candidate against what it recorded, and writes the goldens only if they agree.
"""

from __future__ import annotations

import argparse
import fnmatch
import os
import random
import shutil
import sys
import tempfile
from collections.abc import Callable, Sequence
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from pathlib import Path
from typing import Final, cast

from . import coverage, report
from .generate import generated_steps
from .golden import (
    GOLDEN_DIR,
    GoldenError,
    add_mode_arguments,
    identity_warning,
    loud,
    mode_of,
    names,
    read_file,
    read_meta,
    sha256_file,
    write_set,
)
from .oracle import ORACLE, ORACLE_SOURCE
from .runner import (
    TIMEOUT_SECONDS,
    GoldenCase,
    ScenarioReport,
    case_from_json,
    case_to_json,
    check_golden,
    record_scenario,
    run_scenario,
)
from .scenario import (
    DEFAULT_FOLDERS,
    SCENARIO_DIR,
    FixedSteps,
    Scenario,
    Start,
    StepSource,
    hand_scenarios,
    load_scenario,
)

MOJO_DIR = SCENARIO_DIR.parents[1]
DEFAULT_CANDIDATE = MOJO_DIR / "build" / "vinci"
APP_CS = ORACLE_SOURCE / "src" / "App.cs"
GENERATED_LENGTH = (30, 80)
GENERATED_COUNT: Final = 50
GENERATED_SEED: Final = 1
GOLDEN_CLI: Final = GOLDEN_DIR / "cli"
# The one hand-written scenario that copies the real journal: it cannot be stored in the repo.
REAL_START: Final = "real"


def _length(seed: int, k: int) -> int:
    low, high = GENERATED_LENGTH
    return random.Random(f"length:{seed}:{k}").randint(low, high)


def _scenario_for(seed: int, k: int, length: int) -> Scenario:
    return Scenario(
        name=f"gen-s{seed}-{k:02d}",
        description=f"generated: seed {seed}, scenario {k}, {length} steps",
        start=Start("empty"),
        folders=DEFAULT_FOLDERS,
        steps=(),
    )


@dataclass(frozen=True)
class Generated:
    k: int
    length: int
    scenario: Scenario
    source: StepSource


def _generated(seed: int, count: int) -> list[Generated]:
    out: list[Generated] = []
    for k in range(1, count + 1):
        length = _length(seed, k)
        scenario = _scenario_for(seed, k, length)
        out.append(Generated(k, length, scenario, generated_steps(f"{seed}:{k}", length)))
    return out


@dataclass
class Job:
    scenario: Scenario
    source: StepSource
    case: GoldenCase | None = None


def _check_binary(label: str, path: Path) -> str | None:
    if not path.is_file() or not os.access(path, os.X_OK):
        return f"{label} is not an executable file: {path}"
    return None


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="python -m vinci_diff",
        description="Compare a candidate binary with the goldens (default) or the C# oracle.",
    )
    add_mode_arguments(parser)
    parser.add_argument(
        "--oracle-binary",
        type=Path,
        default=ORACLE,
        help="the C# binary for --oracle and --record (default: VINCI_ORACLE)",
    )
    parser.add_argument(
        "--candidate", type=Path, default=DEFAULT_CANDIDATE, help="the Mojo binary under test"
    )
    parser.add_argument(
        "--scenario",
        default="*",
        metavar="GLOB",
        help="hand-written scenarios whose file name matches (default: all)",
    )
    parser.add_argument(
        "--scenarios-dir", type=Path, default=SCENARIO_DIR, help="where scenario JSON lives"
    )
    parser.add_argument(
        "--generated",
        type=int,
        default=0,
        metavar="N",
        help="also run N generated scenarios (--record: N, default 50)",
    )
    parser.add_argument("--seed", type=int, default=1, help="seed for --generated")
    parser.add_argument(
        "--keep", action="store_true", help="keep the temp directories of passing scenarios"
    )
    parser.add_argument(
        "--jobs", type=int, default=os.cpu_count() or 4, help="scenarios run in parallel"
    )
    parser.add_argument(
        "--coverage",
        action="store_true",
        help="print which commands the hand-written scenarios cover, then exit",
    )
    return parser


def _run_all[R](jobs: list[Job], run: Callable[[Job], R], jobs_parallel: int) -> list[R]:
    """Runs each job with run(job) in a pool; the results come back in job order."""
    with ThreadPoolExecutor(max_workers=max(1, jobs_parallel)) as pool:
        futures = [pool.submit(run, job) for job in jobs]
        return [f.result() for f in futures]


def _print_reports(reports: list[ScenarioReport], seed: int | None, extra: list[str]) -> bool:
    lines: list[str] = list(extra)
    for r in reports:
        if r.divergence is not None:
            lines.extend(report.format_divergence(r.name, r.divergence))
            lines.append("")
        if r.oracle_bug is not None:
            lines.extend(report.format_oracle_bug(r.name, r.oracle_bug))
            lines.append("")
    lines.extend(report.format_summary(reports, seed))
    print("\n".join(lines))
    return all(r.passed for r in reports)


def _golden_jobs(
    directory: Path, glob: str, scenarios_dir: Path
) -> tuple[list[Job], list[str], list[str]]:
    """The jobs a golden set provides, with the lines to print for skips and the problems."""
    jobs: list[Job] = []
    skipped: list[str] = []
    problems: list[str] = []
    for path in sorted(scenarios_dir.glob("*.json")):
        if not fnmatch.fnmatchcase(path.stem, glob):
            continue
        scenario = load_scenario(path)
        if scenario.start.kind == REAL_START:
            skipped.append(
                f"SKIP {scenario.name}: starts from the real journal, which is not in the repo "
                "(run it with --oracle)"
            )
            continue
        golden = read_file(directory, scenario.name)
        if golden is None:
            problems.append(f"{scenario.name}: no golden file (run --record)")
            continue
        if golden.get("scenario_sha256") != sha256_file(path):
            problems.append(f"{scenario.name}: the scenario file changed since it was recorded")
            continue
        case = case_from_json(golden, scenario.name)
        jobs.append(Job(scenario, FixedSteps(scenario.steps), case))
    for name in names(directory):
        if not fnmatch.fnmatchcase(name, glob):
            continue
        golden = read_file(directory, name)
        if golden is None or golden.get("kind") != "generated":
            continue
        seed = int(str(golden["seed"]))
        k = int(str(golden["index"]))
        length = int(str(golden["length"]))
        if _length(seed, k) != length:
            problems.append(
                f"{name}: the generator now gives {_length(seed, k)} steps, not {length}"
            )
            continue
        scenario = _scenario_for(seed, k, length)
        jobs.append(
            Job(scenario, generated_steps(f"{seed}:{k}", length), case_from_json(golden, name))
        )
    return jobs, skipped, problems


def _golden(args: argparse.Namespace, directory: Path) -> int:
    try:
        meta = read_meta(directory)
    except GoldenError as exc:
        print(f"vinci-diff: {exc}", file=sys.stderr)
        return 2
    jobs, skipped, problems = _golden_jobs(directory, args.scenario, args.scenarios_dir)
    if not jobs and not problems:
        print("vinci-diff: no golden scenarios match", file=sys.stderr)
        return 2
    head = [
        (
            f"comparing {len(jobs)} scenarios with the goldens recorded {meta.get('recorded')} "
            f"from commit {str(meta.get('repo_commit'))[:12]}"
        ),
        *skipped,
        *problems,
    ]
    reports = _run_all(
        jobs,
        lambda job: check_golden(
            job.scenario, job.source, job.case or GoldenCase((), ()), args.candidate, args.keep
        ),
        args.jobs,
    )
    generated = cast("dict[str, object]", meta.get("generated", {}))
    seed = int(str(generated["seed"])) if "seed" in generated else None
    ok = _print_reports(reports, seed, head)
    return 0 if ok and not problems else 1


def _record(args: argparse.Namespace, oracle: Path) -> int:
    count = args.generated or GENERATED_COUNT
    seed = args.seed
    staging = Path(tempfile.mkdtemp(prefix="golden-cli-"))
    staged = staging / "cli"
    pending: list[tuple[Scenario, StepSource, dict[str, object]]] = []
    skipped: list[str] = []
    for path in sorted(args.scenarios_dir.glob("*.json")):
        if not fnmatch.fnmatchcase(path.stem, args.scenario):
            continue
        scenario = load_scenario(path)
        if scenario.start.kind == REAL_START:
            skipped.append(f"SKIP {scenario.name}: starts from the real journal (not recorded)")
            continue
        hand_header: dict[str, object] = {"kind": "hand", "scenario_sha256": sha256_file(path)}
        pending.append((scenario, FixedSteps(scenario.steps), hand_header))
    hand_count = len(pending)
    for gen in _generated(seed, count):
        if fnmatch.fnmatchcase(gen.scenario.name, args.scenario):
            header: dict[str, object] = {
                "kind": "generated",
                "seed": seed,
                "index": gen.k,
                "length": gen.length,
            }
            pending.append((gen.scenario, gen.source, header))

    recorded = _run_all(
        [Job(s, src) for s, src, _ in pending],
        lambda job: record_scenario(job.scenario, job.source, oracle, TIMEOUT_SECONDS),
        args.jobs,
    )
    files: dict[str, dict[str, object]] = {}
    for (scenario, _, header), rec in zip(pending, recorded, strict=True):
        if rec.oracle_bug is not None:
            bug = rec.oracle_bug
            print(
                f"record stopped: the oracle failed on {scenario.name} step {bug.index + 1} "
                f"(exit {bug.exit_code}); nothing was written",
                file=sys.stderr,
            )
            return 1
        payload: dict[str, object] = {
            **header,
            "name": scenario.name,
            "description": scenario.description,
            "start": scenario.start.to_json(),
            "folders": list(scenario.folders),
            **case_to_json(GoldenCase(tuple(rec.steps), tuple(rec.outcomes))),
        }
        files[scenario.name] = payload
    write_set(
        staged,
        files,
        "cli",
        oracle,
        {
            "scenario_count": len(files),
            "hand_written": hand_count,
            "generated": {"count": count, "seed": seed},
            "not_recorded": [REAL_START + " start (copies the real journal)"],
        },
    )
    print(f"recorded {len(files)} scenarios into {staged}; checking the candidate against them")
    jobs, _, problems = _golden_jobs(staged, args.scenario, args.scenarios_dir)
    reports = _run_all(
        jobs,
        lambda job: check_golden(
            job.scenario, job.source, job.case or GoldenCase((), ()), args.candidate, args.keep
        ),
        args.jobs,
    )
    agree = _print_reports(reports, seed, [*skipped, *problems])
    if not agree or problems:
        if not args.force:
            print(
                "record refused: the candidate disagrees with the oracle, so the goldens were "
                f"not written (kept at {staged}). Fix the candidate, or run with --force.",
                file=sys.stderr,
            )
            return 1
        loud(
            [
                "--force: writing goldens although the candidate disagrees with the oracle.",
                "The golden check of the candidate will fail until the candidate is fixed.",
            ]
        )
    if GOLDEN_CLI.exists():
        shutil.rmtree(GOLDEN_CLI)
    GOLDEN_CLI.parent.mkdir(parents=True, exist_ok=True)
    shutil.move(str(staged), str(GOLDEN_CLI))
    shutil.rmtree(staging, ignore_errors=True)
    print(f"wrote {len(files)} goldens to {GOLDEN_CLI}")
    return 0


def _live(args: argparse.Namespace, hand: list[Scenario]) -> int:
    jobs: list[tuple[Scenario, StepSource]] = [(sc, FixedSteps(sc.steps)) for sc in hand]
    if args.generated > 0:
        jobs.extend((gen.scenario, gen.source) for gen in _generated(args.seed, args.generated))
    if not jobs:
        print("vinci-diff: no scenarios selected", file=sys.stderr)
        return 2
    print(
        f"running {len(jobs)} scenarios ({len(hand)} hand-written, "
        f"{len(jobs) - len(hand)} generated)",
        flush=True,
    )
    with ThreadPoolExecutor(max_workers=max(1, args.jobs)) as pool:
        futures = [
            pool.submit(
                run_scenario,
                scenario,
                source,
                args.oracle_binary,
                args.candidate,
                args.keep,
                TIMEOUT_SECONDS,
            )
            for scenario, source in jobs
        ]
        reports: list[ScenarioReport] = [f.result() for f in futures]
    seed = args.seed if args.generated > 0 else None
    return 0 if _print_reports(reports, seed, []) else 1


def main(argv: Sequence[str] | None = None) -> int:
    """Runs the harness. A golden set that does not check out exits 2 with the reason."""
    try:
        return _main(argv)
    except GoldenError as exc:
        print(f"vinci-diff: {exc}", file=sys.stderr)
        return 2


def _main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    mode = mode_of(args)
    hand = hand_scenarios(args.scenarios_dir, args.scenario)

    if args.coverage:
        for line in coverage.report(APP_CS, hand):
            print(line)
        return 0

    problem = _check_binary("candidate", args.candidate)
    if problem is not None:
        print(f"vinci-diff: {problem}", file=sys.stderr)
        return 2
    if mode in ("live", "record"):
        problem = _check_binary("oracle", args.oracle_binary)
        if problem is not None:
            print(f"vinci-diff: {problem}", file=sys.stderr)
            return 2
    if mode == "record":
        return _record(args, args.oracle_binary)
    if mode == "live":
        if GOLDEN_CLI.exists():
            warning = identity_warning(read_meta(GOLDEN_CLI), args.oracle_binary)
            if warning:
                print(warning, file=sys.stderr)
        return _live(args, hand)
    return _golden(args, GOLDEN_CLI)


if __name__ == "__main__":
    sys.exit(main())
