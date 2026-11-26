"""Golden files: what the C# oracle printed, saved so the harnesses run without .NET.

mojo/difftest/golden/<harness>/ holds one JSON file per scenario or case and a _meta.json:
the sha256 of the oracle binary and of its vinci.dll, the date, the repository commit, and
the number of files. Each harness writes its set with --record and reads it by default.

The journal only grows, so a journal is stored as a chain: the first step keeps the whole
text or the bytes it appended, and later steps keep only what was appended. journal_text
rebuilds the full text and checks its sha256, so a damaged file fails loudly.

Shared by every harness. It imports none of them.
"""

from __future__ import annotations

import argparse
import datetime
import hashlib
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Final, cast

MOJO_DIR: Final = Path(__file__).resolve().parents[2]
# VINCI_GOLDEN_DIR points the harnesses at another set (the selftest uses a tampered copy).
GOLDEN_DIR: Final = Path(os.environ.get("VINCI_GOLDEN_DIR") or MOJO_DIR / "difftest" / "golden")
META: Final = "_meta.json"


class GoldenError(Exception):
    """A golden set that is missing, or whose contents do not check out."""


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha256_text(text: str) -> str:
    return sha256_bytes(text.encode("utf-8"))


def sha256_file(path: Path) -> str:
    return sha256_bytes(path.read_bytes())


def repo_commit() -> str:
    try:
        done = subprocess.run(
            ["git", "-C", str(MOJO_DIR), "rev-parse", "HEAD"],
            capture_output=True,
            text=True,
            check=True,
        )
    except (OSError, subprocess.CalledProcessError):
        return "unknown"
    return done.stdout.strip()


def oracle_identity(oracle: Path) -> dict[str, str | None]:
    dll = oracle.parent / "vinci.dll"
    return {
        "oracle_sha256": sha256_file(oracle),
        "vinci_dll_sha256": sha256_file(dll) if dll.is_file() else None,
    }


def identity_warning(meta: dict[str, object], oracle: Path) -> str | None:
    """Set when the live oracle is not the binary the goldens were recorded from."""
    if not oracle.is_file():
        return None
    recorded = cast("dict[str, object]", meta.get("oracle", {}))
    now = oracle_identity(oracle)
    if recorded.get("oracle_sha256") == now["oracle_sha256"]:
        return None
    return (
        f"warning: the oracle {oracle} is not the binary the goldens were recorded from "
        f"(recorded {recorded.get('oracle_sha256')}, now {now['oracle_sha256']})"
    )


def write_meta(directory: Path, harness: str, oracle: Path, extra: dict[str, object]) -> None:
    """The set's metadata: the oracle's identity, the date, the commit, and the extra fields."""
    directory.mkdir(parents=True, exist_ok=True)
    count = len(names(directory))
    meta: dict[str, object] = {
        "harness": harness,
        "recorded": datetime.date.today().isoformat(),
        "repo_commit": repo_commit(),
        "oracle": oracle_identity(oracle),
        "files": count,
        **extra,
    }
    write_json(directory / META, meta)


def write_set(
    directory: Path,
    files: dict[str, dict[str, object]],
    harness: str,
    oracle: Path,
    extra: dict[str, object],
) -> None:
    """Replaces the set in directory: one JSON file per name, plus the metadata."""
    if directory.exists():
        shutil.rmtree(directory)
    directory.mkdir(parents=True)
    for name, payload in sorted(files.items()):
        write_json(directory / f"{name}.json", payload)
    write_meta(directory, harness, oracle, extra)


def read_meta(directory: Path) -> dict[str, object]:
    path = directory / META
    if not path.is_file():
        raise GoldenError(f"no golden set at {directory}: run the harness with --record")
    return read_json(path)


def read_file(directory: Path, name: str) -> dict[str, object] | None:
    path = directory / f"{name}.json"
    return read_json(path) if path.is_file() else None


def names(directory: Path) -> list[str]:
    return sorted(p.stem for p in directory.glob("*.json") if p.name != META)


def write_json(path: Path, data: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    text = json.dumps(data, indent=1, ensure_ascii=False, sort_keys=True) + "\n"
    path.write_text(text, encoding="utf-8")


def read_json(path: Path) -> dict[str, object]:
    raw: object = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(raw, dict):
        raise GoldenError(f"{path}: expected a JSON object")
    return cast("dict[str, object]", raw)


def journal_step(previous: str, now: str) -> dict[str, object]:
    """The journal after one step, as the bytes it appended to the previous one."""
    digest = sha256_text(now)
    if now.startswith(previous):
        return {"sha256": digest, "append": now[len(previous) :]}
    return {"sha256": digest, "replace": now}


def journal_text(previous: str, step: dict[str, object], where: str) -> str:
    """The journal after a step, rebuilt from its chain link. Raises when the hash differs."""
    if "replace" in step:
        text = str(step["replace"])
    else:
        text = previous + str(step.get("append", ""))
    if sha256_text(text) != step.get("sha256"):
        raise GoldenError(f"{where}: the journal does not match its recorded sha256")
    return text


def add_mode_arguments(parser: argparse.ArgumentParser) -> None:
    group = parser.add_mutually_exclusive_group()
    group.add_argument(
        "--oracle",
        action="store_true",
        help="compare live with the C# oracle (needs .NET and VINCI_ORACLE), not the goldens",
    )
    group.add_argument(
        "--record",
        action="store_true",
        help="regenerate the goldens from the live oracle (needs .NET); refuses if the candidate disagrees",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="with --record: write the goldens even though the candidate disagrees (prints a warning)",
    )


def mode_of(args: argparse.Namespace) -> str:
    if getattr(args, "record", False):
        return "record"
    if getattr(args, "oracle", False):
        return "live"
    return "golden"


def loud(lines: list[str]) -> None:
    """Prints a warning block to standard error."""
    bar = "!" * 72
    print(bar, file=sys.stderr)
    for line in lines:
        print(f"!! {line}", file=sys.stderr)
    print(bar, file=sys.stderr)
