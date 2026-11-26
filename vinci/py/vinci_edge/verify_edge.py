"""The index check of `vinci verify --index`: a rebuild from the journal, compared with the stored index.

Mojo rebuilds the journal's facts into a temporary folder (index_build.mojo) and calls this edge
with both paths. The stored index is opened read-only, so the check never writes the real index.
Stdlib only. Each function returns a status pair, as the other edges do.
"""

from __future__ import annotations

import shutil
import sqlite3
import tempfile
from pathlib import Path
from typing import Final

SAMPLE: Final = 5
CURRENT_FACTS: Final = (
    "SELECT source, record_id, version_hash FROM facts"
    " WHERE source = 'journal' AND valid_until IS NULL"
)


def scratch_folder() -> tuple[str, str]:
    """A new empty folder for the rebuild, under the system's temporary directory."""
    try:
        return ("ok", tempfile.mkdtemp(prefix="vinci-verify-"))
    except OSError as err:
        return ("error", f"cannot make a temporary folder: {err}")


def remove_folder(path: str) -> None:
    """Removes a folder made by scratch_folder. Nothing to do if it is gone."""
    shutil.rmtree(path, ignore_errors=True)


def _current(path: str) -> set[tuple[str, str, str]]:
    connection = sqlite3.connect(f"file:{Path(path).as_posix()}?mode=ro", uri=True)
    try:
        rows = connection.execute(CURRENT_FACTS).fetchall()
    finally:
        connection.close()
    return {(str(row[0]), str(row[1]), str(row[2])) for row in rows}


def compare(stored: str, rebuilt: str) -> tuple[str, str, int]:
    """Compares the journal facts that are current in two indexes.

    Returns (status, text, differences). status is "same", "differs", "missing" (no stored
    index) or "error" (a file could not be read). text is for the summary.
    """
    if not Path(stored).is_file():
        return ("missing", f"there is no stored index at {stored}; run vinci index update", 0)
    try:
        ours = _current(stored)
        theirs = _current(rebuilt)
    except sqlite3.Error as err:
        return ("error", f"cannot read an index: {err}", 0)
    only_stored = sorted(ours - theirs)
    only_rebuilt = sorted(theirs - ours)
    text = (
        f"index: {len(ours)} current journal facts stored, {len(theirs)} in a rebuild; "
        f"{len(only_stored)} only in the stored index, {len(only_rebuilt)} only in the rebuild"
    )
    for label, items in (("stored only", only_stored), ("rebuild only", only_rebuilt)):
        for _source, record_id, digest in items[:SAMPLE]:
            text += f"\n  {label}: {record_id} ({digest[:12]})"
    differences = len(only_stored) + len(only_rebuilt)
    return ("same" if differences == 0 else "differs", text, differences)
