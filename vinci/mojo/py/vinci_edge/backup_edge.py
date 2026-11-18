"""`vinci backup`: a consistent copy of journal.jsonl, with a sha256 manifest.

Backup is a reader: it takes no lock, and cuts the copy after the journal's last newline, so it
never catches a line that is half written. The bytes are read once, written to a temporary file in the
destination, read back and compared with the hash of the bytes read, and only then linked to
their final name. The final name is made with link(2), which refuses to overwrite, so an
existing backup is never replaced. A manifest line in the format `sha256sum -c` reads is written
beside it.

Pruning (--keep N) removes only files this command could have made: journal-<UTC time>.jsonl,
optionally with a -<n> suffix for a second copy in the same second, and their .sha256 manifests.

Mojo calls backup() once and gets ("ok", report) or ("error", message). Nothing is raised.
"""

from __future__ import annotations

import hashlib
import os
import re
from datetime import UTC, datetime
from pathlib import Path
from typing import Final

NAME_RE: Final = re.compile(r"^journal-(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}Z)(?:-(\d+))?\.jsonl$")
MANIFEST_SUFFIX: Final = ".sha256"
MAX_SUFFIX: Final = 999


def stamp_of(now_iso: str) -> str:
    """The UTC time of an ISO 8601 instant, as 2026-11-18T19-30-00Z (colons are left out of
    file names, and the rest is ISO 8601)."""
    moment = datetime.fromisoformat(now_iso).astimezone(UTC)
    return moment.strftime("%Y-%m-%dT%H-%M-%SZ")


def sha256_of(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def backup_names(directory: Path) -> list[tuple[tuple[str, int], Path]]:
    """The backup files this command made in directory, oldest first, with their sort key."""
    found: list[tuple[tuple[str, int], Path]] = []
    if not directory.is_dir():
        return found
    for entry in directory.iterdir():
        match = NAME_RE.match(entry.name)
        if match is None or not entry.is_file():
            continue
        suffix = int(match.group(2)) if match.group(2) is not None else 0
        found.append(((match.group(1), suffix), entry))
    found.sort(key=lambda item: item[0])
    return found


def prune(directory: Path, keep: int) -> list[str]:
    """Removes the oldest backups beyond the newest keep, with their manifests. Returns the names
    removed."""
    removed: list[str] = []
    files = backup_names(directory)
    for _, path in files[: max(0, len(files) - keep)]:
        manifest = path.with_name(path.name + MANIFEST_SUFFIX)
        path.unlink()
        removed.append(path.name)
        if manifest.exists():
            manifest.unlink()
    return removed


def write_new(directory: Path, data: bytes, stamp: str) -> tuple[Path, str]:
    """Writes data as a new backup in directory and returns its path and name. Never replaces a
    file: a name in use gets a -<n> suffix, and link(2) refuses an existing name."""
    temporary = directory / f".journal-{stamp}.{os.getpid()}.tmp"
    with open(temporary, "xb") as handle:
        handle.write(data)
        handle.flush()
        os.fsync(handle.fileno())
    try:
        for n in range(MAX_SUFFIX + 1):
            name = f"journal-{stamp}.jsonl" if n == 0 else f"journal-{stamp}-{n}.jsonl"
            final = directory / name
            try:
                os.link(temporary, final)
            except FileExistsError:
                continue
            return (final, name)
        raise OSError(f"no free name for {stamp}")
    finally:
        temporary.unlink()


def backup(journal: str, directory: str, keep: int, now_iso: str) -> tuple[str, str]:
    """Copies the journal to directory. keep > 0 prunes beyond the newest keep backups.

    Returns ("ok", report lines) or ("error", message).
    """
    source = Path(journal)
    target = Path(directory)
    try:
        stamp = stamp_of(now_iso)
    except ValueError as err:
        return ("error", f"the time is not ISO 8601: {err}")
    try:
        if not source.is_file():
            return ("error", f"there is no journal at {source}")
        # A reader: no lock. Lines are appended whole, so the copy is cut after its last newline:
        # a line a writer is still writing is left for the next backup.
        raw = source.read_bytes()
        end = raw.rfind(b"\n") + 1
        data = raw[:end]
        digest = sha256_of(data)
        target.mkdir(parents=True, exist_ok=True)
        final, name = write_new(target, data, stamp)
        copied = final.read_bytes()
        if sha256_of(copied) != digest or len(copied) != len(data):
            final.unlink()
            return ("error", f"the copy {final} does not match the journal's bytes")
        manifest = final.with_name(name + MANIFEST_SUFFIX)
        with open(manifest, "x", encoding="utf-8") as handle:
            handle.write(f"{digest}  {name}\n")
            handle.flush()
            os.fsync(handle.fileno())
    except OSError as err:
        return ("error", f"backup failed: {err}")
    report = f"backup: {final}\nbytes: {len(data)}\nsha256: {digest}\nmanifest: {manifest}"
    if keep > 0:
        try:
            removed = prune(target, keep)
        except OSError as err:
            return ("error", f"the backup was written, but pruning failed: {err}\n{report}")
        report += f"\npruned: {len(removed)} older backup(s), keeping {keep}"
    return ("ok", report)
