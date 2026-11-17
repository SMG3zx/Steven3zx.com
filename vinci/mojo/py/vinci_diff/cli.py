"""Command line: pick scenarios, run them in parallel, print the report, set the exit code.

uv run python -m vinci_diff --candidate ../build/vinci --generated 50 --seed 1
"""

import argparse
import os
import random
import sys
from collections.abc import Sequence
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

from . import coverage, report
from .generate import generated_steps
from .runner import TIMEOUT_SECONDS, ScenarioReport, run_scenario
from .scenario import (
    DEFAULT_FOLDERS,
    SCENARIO_DIR,
    FixedSteps,
    Scenario,
    Start,
    StepSource,
    hand_scenarios,
)

MOJO_DIR = SCENARIO_DIR.parents[1]
DEFAULT_ORACLE = MOJO_DIR / "build" / "oracle" / "vinci"
DEFAULT_CANDIDATE = MOJO_DIR / "build" / "vinci"
APP_CS = MOJO_DIR.parent / "src" / "App.cs"
GENERATED_LENGTH = (30, 80)


def _generated(seed: int, count: int) -> list[tuple[Scenario, StepSource]]:
    out: list[tuple[Scenario, StepSource]] = []
    low, high = GENERATED_LENGTH
    for k in range(1, count + 1):
        length = random.Random(f"length:{seed}:{k}").randint(low, high)
        name = f"gen-s{seed}-{k:02d}"
        scenario = Scenario(
            name=name,
            description=f"generated: seed {seed}, scenario {k}, {length} steps",
            start=Start("empty"),
            folders=DEFAULT_FOLDERS,
            steps=(),
        )
        out.append((scenario, generated_steps(f"{seed}:{k}", length)))
    return out


def _check_binary(label: str, path: Path) -> str | None:
    if not path.is_file() or not os.access(path, os.X_OK):
        return f"{label} is not an executable file: {path}"
    return None


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="python -m vinci_diff",
        description="Compare the C# oracle with a candidate binary, one command at a time.",
    )
    parser.add_argument("--oracle", type=Path, default=DEFAULT_ORACLE, help="the C# binary")
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
        "--generated", type=int, default=0, metavar="N", help="also run N generated scenarios"
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


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    hand = hand_scenarios(args.scenarios_dir, args.scenario)

    if args.coverage:
        for line in coverage.report(APP_CS, hand):
            print(line)
        return 0

    for label, binary in (("oracle", args.oracle), ("candidate", args.candidate)):
        problem = _check_binary(label, binary)
        if problem is not None:
            print(f"vinci-diff: {problem}", file=sys.stderr)
            return 2

    jobs: list[tuple[Scenario, StepSource]] = [(sc, FixedSteps(sc.steps)) for sc in hand]
    if args.generated > 0:
        jobs.extend(_generated(args.seed, args.generated))
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
                args.oracle,
                args.candidate,
                args.keep,
                TIMEOUT_SECONDS,
            )
            for scenario, source in jobs
        ]
        reports: list[ScenarioReport] = [f.result() for f in futures]

    lines: list[str] = []
    for r in reports:
        if r.divergence is not None:
            lines.extend(report.format_divergence(r.name, r.divergence))
            lines.append("")
        if r.oracle_bug is not None:
            lines.extend(report.format_oracle_bug(r.name, r.oracle_bug))
            lines.append("")
    seed = args.seed if args.generated > 0 else None
    lines.extend(report.format_summary(reports, seed))
    print("\n".join(lines))
    return 0 if all(r.passed for r in reports) else 1
