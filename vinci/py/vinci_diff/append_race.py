"""The append race test: several vinci processes mint ids into one journal at the same time.

Each worker runs the real binary `idea` command 100 times against one temporary VINCI_HOME. An
idea mints its id from the journal's own ideas (i<n>), so two processes that read before either
appends would mint the same id. The test then checks the journal:

- every line parses as a JSON object with an `at` and a `kind`;
- no line is torn (the file ends with a newline, and the line count is the number of ideas);
- every idea id is unique;
- `vinci verify` on the same home passes.

Usage (from mojo/py, with the binary built by the caller):

    uv run python -m vinci_diff.append_race --binary /abs/path/vinci [--workers 4] [--lines 100]
    uv run python -m vinci_diff.append_race --binary /abs/path/vinci --unlocked

--unlocked sets VINCI_TEST_NO_JOURNAL_LOCK=1 in the binary's environment. That switch turns the
journal lock off. It exists only for this test, so the test can show that the lock is what
prevents the duplicate ids. The default run has no such switch.

Exit 0 when the journal is clean and verify passes; exit 1 otherwise. Each run uses its own
temporary home. The real ~/.vinci is never touched.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from pathlib import Path
from typing import Final, cast

NOW: Final = "2026-11-20T12:00:00-06:00"
TEST_OFF_ENV: Final = "VINCI_TEST_NO_JOURNAL_LOCK"
RUN_TIMEOUT_S: Final = 120


@dataclass
class Report:
    """What the race left behind, and what verify said."""

    lines_expected: int = 0
    lines: int = 0
    ideas: int = 0
    unparsed: list[str] = field(default_factory=list[str])
    duplicates: list[str] = field(default_factory=list[str])
    failed_runs: list[str] = field(default_factory=list[str])
    reader_runs: int = 0
    reader_failures: list[str] = field(default_factory=list[str])
    verify_code: int = -1
    verify_output: str = ""

    def clean(self) -> bool:
        return (
            not self.unparsed
            and not self.duplicates
            and not self.failed_runs
            and not self.reader_failures
            and self.ideas == self.lines_expected
            and self.verify_code == 0
        )


def child_env(home: Path, unlocked: bool, tracking: bool = False) -> dict[str, str]:
    """The environment of a vinci run: its own home and no env file. The tracker is off, except
    for the readers, which run with it on as a person's commands do."""
    env = dict(os.environ)
    env["VINCI_HOME"] = str(home)
    if tracking:
        env.pop("VINCI_NO_TRACK", None)
    else:
        env["VINCI_NO_TRACK"] = "1"
    env["VINCI_NOW"] = NOW
    env["VINCI_ENV_FILE"] = str(home / "empty.env")
    env.pop("VIRTUAL_ENV", None)
    if unlocked:
        env[TEST_OFF_ENV] = "1"
    else:
        env.pop(TEST_OFF_ENV, None)
    return env


def worker(binary: Path, home: Path, unlocked: bool, worker_id: int, lines: int) -> list[str]:
    """Runs `idea` lines times. Returns the failures, one text per failed run."""
    env = child_env(home, unlocked)
    failures: list[str] = []
    for n in range(lines):
        title = f"race worker {worker_id} line {n}"
        done = subprocess.run(
            [str(binary), "idea", title],
            env=env,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=RUN_TIMEOUT_S,
            check=False,
        )
        if done.returncode != 0:
            text = done.stdout.decode("utf-8", errors="replace").strip()
            failures.append(f"worker {worker_id} line {n}: exit {done.returncode}: {text}")
    return failures


def check_journal(journal: Path, expected: int) -> Report:
    """The journal's lines and ids. Torn or non-JSON lines are listed, not repaired."""
    report = Report(lines_expected=expected)
    if not journal.exists():
        report.unparsed.append("the journal was not written")
        return report
    raw = journal.read_bytes()
    if raw and not raw.endswith(b"\n"):
        report.unparsed.append("the journal does not end with a newline (a torn last line)")
    ids: Counter[str] = Counter()
    for number, line in enumerate(raw.decode("utf-8", errors="replace").split("\n"), start=1):
        if line == "":
            continue
        report.lines += 1
        try:
            parsed: object = json.loads(line)
        except json.JSONDecodeError:
            report.unparsed.append(f"line {number}: not JSON: {line[:80]!r}")
            continue
        if not isinstance(parsed, dict):
            report.unparsed.append(f"line {number}: not an event: {line[:80]!r}")
            continue
        row = cast("dict[str, object]", parsed)
        if "at" not in row or not isinstance(row.get("kind"), str):
            report.unparsed.append(f"line {number}: not an event: {line[:80]!r}")
            continue
        if row["kind"] == "idea":
            report.ideas += 1
            ids[str(row.get("id", ""))] += 1
    for name, count in sorted(ids.items()):
        if count > 1:
            report.duplicates.append(f"{name} x{count}")
    return report


def run_verify(binary: Path, home: Path, report: Report) -> None:
    """vinci verify on the same home. Its exit code and output go in the report."""
    done = subprocess.run(
        [str(binary), "verify"],
        env=child_env(home, unlocked=False),
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=RUN_TIMEOUT_S,
        check=False,
    )
    report.verify_code = done.returncode
    report.verify_output = done.stdout.decode("utf-8", errors="replace")


def reader(binary: Path, home: Path, rounds: int) -> list[str]:
    """Runs `ideas` and `stats` rounds times, with the tracker on, while the writers run. A reader
    must never fail: it never waits for a writer and ignores a line still being written."""
    env = child_env(home, unlocked=False, tracking=True)
    failures: list[str] = []
    for n in range(rounds):
        for word in ("ideas", "stats"):
            done = subprocess.run(
                [str(binary), word],
                env=env,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                timeout=RUN_TIMEOUT_S,
                check=False,
            )
            if done.returncode != 0:
                text = done.stdout.decode("utf-8", errors="replace").strip()
                failures.append(f"round {n} {word}: exit {done.returncode}: {text}")
    return failures


def run_race(
    binary: Path,
    workers: int,
    lines: int,
    unlocked: bool,
    keep: Path | None,
    readers: int = 2,
    reader_rounds: int = 40,
) -> Report:
    """One race in a fresh home. keep, when given, is a folder that keeps the home for inspection.
    readers reader processes run alongside the writers."""
    base = keep if keep is not None else Path(tempfile.mkdtemp(prefix="vinci-race-"))
    home = base / "home"
    home.mkdir(parents=True, exist_ok=True)
    (home / "empty.env").write_text("", encoding="utf-8")
    with ThreadPoolExecutor(max_workers=workers + readers) as pool:
        futures = [pool.submit(worker, binary, home, unlocked, w, lines) for w in range(workers)]
        reader_futures = [pool.submit(reader, binary, home, reader_rounds) for _ in range(readers)]
        failures: list[str] = []
        for future in futures:
            failures.extend(future.result())
        reader_failures: list[str] = []
        for future in reader_futures:
            reader_failures.extend(future.result())
    report = check_journal(home / "journal.jsonl", workers * lines)
    report.failed_runs = failures
    report.reader_runs = readers * reader_rounds * 2
    report.reader_failures = reader_failures
    run_verify(binary, home, report)
    return report


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="vinci append race test")
    parser.add_argument(
        "--binary", required=True, type=Path, help="absolute path of the vinci binary"
    )
    parser.add_argument("--workers", type=int, default=4)
    parser.add_argument("--lines", type=int, default=100)
    parser.add_argument(
        "--readers", type=int, default=2, help="reader processes alongside the writers"
    )
    parser.add_argument("--reader-rounds", type=int, default=40)
    parser.add_argument(
        "--unlocked", action="store_true", help="test only: turn the journal lock off"
    )
    parser.add_argument(
        "--keep", type=Path, default=None, help="keep the temporary home in this folder"
    )
    parser.add_argument(
        "--label", default="binary as built", help="the binary's name in the output"
    )
    args = parser.parse_args(argv)
    binary = args.binary.resolve()
    if not binary.is_file():
        print(f"append_race: no binary at {binary}", file=sys.stderr)
        return 1
    report = run_race(
        binary, args.workers, args.lines, args.unlocked, args.keep, args.readers, args.reader_rounds
    )
    mode = f"{args.label}, unlocked (test switch)" if args.unlocked else args.label
    print(
        f"append_race {mode}: {args.workers} workers x {args.lines} idea lines; "
        f"journal lines {report.lines}, ideas {report.ideas} (expected {report.lines_expected}), "
        f"duplicate ids {len(report.duplicates)}, unparsed lines {len(report.unparsed)}, "
        f"failed runs {len(report.failed_runs)}, reader runs {report.reader_runs} "
        f"(reader failures {len(report.reader_failures)}), verify exit {report.verify_code}"
    )
    for item in report.reader_failures[:5]:
        print(f"  reader failure: {item}")
    for item in report.duplicates[:10]:
        print(f"  duplicate: {item}")
    for item in report.unparsed[:10]:
        print(f"  unparsed: {item}")
    for item in report.failed_runs[:5]:
        print(f"  failed run: {item}")
    if report.verify_code != 0:
        print("  verify output:")
        print(report.verify_output.rstrip())
    return 0 if report.clean() else 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
