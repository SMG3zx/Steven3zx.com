"""The text the harness prints: divergences, oracle bugs, and the summary."""

import json
from collections.abc import Sequence

from .runner import Divergence, OracleBug, ScenarioReport

MAX_DIFF_LINES = 40


def _argv(argv: tuple[str, ...]) -> str:
    return json.dumps(list(argv), ensure_ascii=False)


def format_divergence(name: str, div: Divergence) -> list[str]:
    head = f"DIVERGED  {name}  step {div.index + 1}  now {div.step.now}"
    lines = [head, f"  argv:     {_argv(div.step.argv)}"]
    if div.step.stdin:
        lines.append(f"  stdin:    {json.dumps(div.step.stdin, ensure_ascii=False)}")
    also = f"  (also differs: {', '.join(div.other_channels)})" if div.other_channels else ""
    lines.append(f"  channel:  {div.channel}{also}")
    shown = div.diff_lines[:MAX_DIFF_LINES]
    lines.extend(f"    {line}" for line in shown)
    if len(div.diff_lines) > MAX_DIFF_LINES:
        lines.append(f"    ... {len(div.diff_lines) - MAX_DIFF_LINES} more lines trimmed")
    return lines


def format_oracle_bug(name: str, bug: OracleBug) -> list[str]:
    code = "timed out" if bug.exit_code is None else f"exit {bug.exit_code}"
    lines = [
        f"ORACLE BUG  {name}  step {bug.index + 1}  now {bug.step.now}",
        f"  argv:     {_argv(bug.step.argv)}",
        f"  oracle {code} (only 0 and 1 are allowed); stderr:",
    ]
    lines.extend(f"    {line}" for line in bug.stderr_head)
    return lines


def format_summary(reports: Sequence[ScenarioReport], seed: int | None) -> list[str]:
    steps_run = sum(r.steps_run for r in reports)
    steps_matching = sum(r.steps_matching for r in reports)
    failing = [r for r in reports if not r.passed]
    diverged = sum(1 for r in reports if r.divergence is not None)
    bugs = sum(1 for r in reports if r.oracle_bug is not None)
    generated = "" if seed is None else f"  (generated, seed {seed})"
    lines = [
        "",
        "summary",
        f"  scenarios run:    {len(reports)}{generated}",
        f"  steps run:        {steps_run}",
        f"  steps matching:   {steps_matching}",
        f"  diverged:         {diverged}",
        f"  oracle bugs:      {bugs}",
    ]
    if diverged:
        lines.append("  first divergence per failing scenario:")
    for r in failing:
        d = r.divergence
        if d is not None:
            lines.append(
                f"    {r.name}: step {d.index + 1} {_argv(d.step.argv)} ({d.channel})"
                f"  repro: {r.repro}"
            )
    if bugs:
        lines.append("  oracle bugs (scenario stopped at the crash):")
    for r in failing:
        b = r.oracle_bug
        if b is not None:
            lines.append(f"    {r.name}: step {b.index + 1} {_argv(b.step.argv)}  repro: {r.repro}")
    return lines
