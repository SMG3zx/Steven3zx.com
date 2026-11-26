"""`vinci backup [--dir DIR] [--keep N]` (K0b): a consistent copy of journal.jsonl with a sha256
manifest. The copy is made under the journal's append lock by vinci_edge/backup_edge.py, and
its bytes are checked before the copy gets its name. Nothing here writes the journal.

Port of mojo/src/cmd_backup.mojo.
"""

from __future__ import annotations

from vinci_edge import backup_edge

from .session import user_error
from .timeutil import Instant, format_instant


def backup_command(args: list[str], home: str, journal: str, now: Instant) -> None:
    directory = home + "/backups/journal"
    keep = 0
    i = 0
    while i < len(args):
        a = args[i]
        if a == "--dir" and i + 1 < len(args):
            directory = args[i + 1]
            i += 2
        elif a == "--keep" and i + 1 < len(args):
            try:
                keep = int(args[i + 1])
            except ValueError:
                raise user_error("--keep must be a whole number") from None
            if keep < 1:
                raise user_error("--keep must be at least 1")
            i += 2
        else:
            raise user_error("backup takes --dir DIR and --keep N, got '" + a + "'")
    status, message = backup_edge.backup(journal, directory, keep, format_instant(now))
    if status != "ok":
        raise user_error(message)
    print(message)
