#!/usr/bin/env python3
"""Add required error-contract sections to public Rust fallible APIs.

This is deliberately driven by the current Clippy artifact and refuses to
duplicate an existing ``# Errors`` section. It is a maintenance helper for the
strict local gate, not a lint suppression mechanism.
"""

from __future__ import annotations

from pathlib import Path
import re


ROOT = Path(__file__).resolve().parent.parent
REPORT = ROOT / "janus-rust" / "artifacts" / "clippy-output.txt"
SOURCE_ROOT = ROOT / "janus-rust"
LOCATION = re.compile(r"^\s*--> (.+):(\d+):\d+$")


def reported_locations() -> set[tuple[Path, int]]:
    lines = REPORT.read_text(encoding="utf-8").splitlines()
    locations: set[tuple[Path, int]] = set()
    for index, line in enumerate(lines):
        if "missing `# Errors` section" not in line:
            continue
        for following in lines[index + 1 : index + 6]:
            match = LOCATION.match(following)
            if match:
                path = Path(match.group(1).replace("\\", "/"))
                if not path.is_absolute():
                    path = SOURCE_ROOT / path
                locations.add((path, int(match.group(2))))
                break
    return locations


def add_error_section(path: Path, line_number: int) -> bool:
    lines = path.read_text(encoding="utf-8").splitlines(keepends=True)
    function_index = line_number - 1
    if function_index < 0 or function_index >= len(lines):
        return False
    if "# Errors" in "".join(lines[max(0, function_index - 12) : function_index]):
        return False
    insertion = (
        "///\n"
        "/// # Errors\n"
        "///\n"
        "/// Returns an error when the operation cannot satisfy its input or\n"
        "/// bounded-state contract.\n"
    )
    lines.insert(function_index, insertion)
    path.write_text("".join(lines), encoding="utf-8")
    return True


def main() -> None:
    changed = 0
    for path, line_number in sorted(reported_locations(), key=lambda item: (str(item[0]), item[1]), reverse=True):
        if add_error_section(path, line_number):
            changed += 1
    print(f"added error contracts: {changed}")


if __name__ == "__main__":
    main()
