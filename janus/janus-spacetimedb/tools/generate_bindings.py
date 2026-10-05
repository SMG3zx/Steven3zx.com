"""Generate Rust client bindings for the Janus SpacetimeDB module."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_OUTPUT = ROOT.parent / "janus-rust" / "src" / "module_bindings"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument(
        "--check-only",
        action="store_true",
        help="report whether the SpacetimeDB CLI is available without generating files",
    )
    parser.add_argument(
        "--require-installed",
        action="store_true",
        help="fail when the CLI is unavailable; intended for the production gate",
    )
    parser.add_argument(
        "--verify-output",
        action="store_true",
        help="fail unless generated Rust bindings already exist in the output directory",
    )
    args = parser.parse_args()

    generated_files = tuple(args.output.glob("*.rs"))
    cli = shutil.which("spacetime")
    if cli is None and os.name == "nt":
        installed_cli = (
            Path(os.environ.get("LOCALAPPDATA", ""))
            / "SpacetimeDB"
            / "spacetime.exe"
        )
        if installed_cli.is_file():
            cli = str(installed_cli)
    if cli is None:
        print(
            "SpacetimeDB CLI is not installed; install it before generating bindings.",
            file=sys.stderr,
        )
        if args.verify_output and not generated_files:
            print(
                f"No generated Rust bindings found in {args.output}; "
                "the production gate requires both the CLI and committed bindings.",
                file=sys.stderr,
            )
        return 2 if args.require_installed or not args.check_only else 0
    if args.verify_output:
        if not generated_files:
            print(
                f"No generated Rust bindings found in {args.output}; run this tool without --verify-output.",
                file=sys.stderr,
            )
            return 2
    if args.check_only:
        print(f"SpacetimeDB CLI: {cli}")
        return 0

    args.output.mkdir(parents=True, exist_ok=True)
    command = [
        cli,
        "generate",
        "--yes",
        "--lang",
        "rust",
        "--out-dir",
        str(args.output),
        "--module-path",
        str(ROOT),
    ]
    print("+", " ".join(command), flush=True)
    return subprocess.run(command, cwd=ROOT.parent).returncode


if __name__ == "__main__":
    raise SystemExit(main())
