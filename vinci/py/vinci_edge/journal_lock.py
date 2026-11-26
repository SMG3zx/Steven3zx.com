"""The journal's append lock: one exclusive flock per journal file, for writers only.

A writer (a command that appends, or the tracker when it has facts to write) holds the lock
from its first read until its last append, so an id minted from the journal's own ids cannot be
taken by another process in between. Readers never take it, and nothing here is called for a
read: a reader ignores a last line that has no newline yet (journal.mojo Journal.read). The
lock file is journal.jsonl.lock beside the journal. The kernel drops an flock when its process
exits, so a crash never leaves a lock behind.

Mojo calls each function once per request and gets a status pair back, never an exception:
("ok", "") or ("error", message). The lock is re-entrant inside one process: a nested take counts
the depth, so the same writer can take it again without waiting for itself.

VINCI_TEST_NO_JOURNAL_LOCK=1 turns the lock off. It exists only so the append race test can
show that the unlocked code loses ids (scripts/append_race.py). Nothing else sets it.
"""

from __future__ import annotations

import fcntl
import os
import time

LOCK_SUFFIX = ".lock"
LOCK_TIMEOUT_S = 30.0
LOCK_POLL_S = 0.01
TEST_OFF_ENV = "VINCI_TEST_NO_JOURNAL_LOCK"

# journal path -> (file descriptor, depth). Only this process reads it.
_held: dict[str, tuple[int, int]] = {}


def _key(journal: str) -> str:
    return os.path.abspath(journal)


def _lock_off() -> bool:
    return os.environ.get(TEST_OFF_ENV, "") == "1"


def acquire(journal: str) -> tuple[str, str]:
    """Takes the exclusive lock on journal's lock file, waiting up to LOCK_TIMEOUT_S.

    Creates the journal's folder if it is missing. Taking it again in the same process only
    counts the depth; release undoes one level.
    """
    if _lock_off():
        return ("ok", "")
    key = _key(journal)
    if key in _held:
        fd, depth = _held[key]
        _held[key] = (fd, depth + 1)
        return ("ok", "")
    try:
        os.makedirs(os.path.dirname(key), exist_ok=True)
        fd = os.open(key + LOCK_SUFFIX, os.O_RDWR | os.O_CREAT, 0o644)
    except OSError as err:
        return ("error", f"cannot open the journal lock: {err}")
    deadline = time.monotonic() + LOCK_TIMEOUT_S
    while True:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            break
        except BlockingIOError:
            if time.monotonic() >= deadline:
                os.close(fd)
                return (
                    "error",
                    f"the journal is locked by another vinci for over {LOCK_TIMEOUT_S:g} s",
                )
            time.sleep(LOCK_POLL_S)
        except OSError as err:
            os.close(fd)
            return ("error", f"cannot lock the journal: {err}")
    _held[key] = (fd, 1)
    return ("ok", "")


def release(journal: str) -> tuple[str, str]:
    """Undoes one acquire. The last release unlocks and closes the lock file."""
    if _lock_off():
        return ("ok", "")
    key = _key(journal)
    if key not in _held:
        return ("error", "the journal lock is not held")
    fd, depth = _held[key]
    if depth > 1:
        _held[key] = (fd, depth - 1)
        return ("ok", "")
    del _held[key]
    try:
        fcntl.flock(fd, fcntl.LOCK_UN)
    finally:
        os.close(fd)
    return ("ok", "")


def append_durable(journal: str, text: str) -> tuple[str, str]:
    """Appends text (whole lines, each ending in a newline) and fsyncs before returning.

    The caller must hold the lock. The file is opened in append mode and written in one call.
    """
    if not _lock_off() and _key(journal) not in _held:
        return ("error", "journal lines were written without holding the journal lock")
    try:
        with open(journal, "ab") as handle:
            handle.write(text.encode("utf-8"))
            handle.flush()
            os.fsync(handle.fileno())
    except OSError as err:
        return ("error", f"cannot append to the journal: {err}")
    return ("ok", "")
