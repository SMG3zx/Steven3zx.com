"""Which commands the hand-written scenarios run, read from the Dispatch switch in App.cs."""

import re
from collections.abc import Sequence
from pathlib import Path

from .scenario import Scenario

# Dispatch commands that are out of M2 scope, so no scenario has to cover them.
OUT_OF_SCOPE = ("chat", "serve", "memory", "sync")
QUOTED = re.compile(r'"([^"]+)"')


def dispatch_commands(app_cs: Path) -> list[str]:
    """Every command name in the switch on args[0] in App.cs, in source order."""
    lines = app_cs.read_text(encoding="utf-8").splitlines()
    start = next(i for i, line in enumerate(lines) if "switch (args[0])" in line)
    end = next(i for i in range(start, len(lines)) if lines[i].strip() == "default:")
    names: list[str] = []
    for line in lines[start:end]:
        if not line.strip().startswith("case "):
            continue
        for name in QUOTED.findall(line):
            if name not in names:
                names.append(name)
    return names


def report(app_cs: Path, scenarios: Sequence[Scenario]) -> list[str]:
    commands = dispatch_commands(app_cs)
    covered: dict[str, list[str]] = {name: [] for name in commands}
    unknown: list[str] = []
    for scenario in scenarios:
        seen: set[str] = set()
        for step in scenario.steps:
            head = step.argv[0]
            if head in covered:
                seen.add(head)
            else:
                unknown.append(f"{scenario.name}: {head}")
        for head in sorted(seen):
            covered[head].append(scenario.name)

    lines = ["command coverage (hand-written scenarios):"]
    for name in commands:
        if name in OUT_OF_SCOPE:
            lines.append(f"  {name:<12} out of M2 scope")
        elif covered[name]:
            lines.append(f"  {name:<12} {', '.join(covered[name])}")
        else:
            lines.append(f"  {name:<12} NOT COVERED")
    not_covered = [n for n in commands if n not in OUT_OF_SCOPE and not covered[n]]
    lines.append(f"not covered: {', '.join(not_covered) if not_covered else 'none'}")
    lines.append(
        "out of scope: "
        + ", ".join(OUT_OF_SCOPE)
        + " (and the no-argument 'vinci', which starts the chat)"
    )
    if unknown:
        lines.append("unknown commands used on purpose (expected errors): " + "; ".join(unknown))
    return lines
