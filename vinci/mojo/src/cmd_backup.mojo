# `vinci backup [--dir DIR] [--keep N]` (K0b): a consistent copy of journal.jsonl with a sha256
# manifest. The copy is made under the journal's append lock by py/vinci_edge/backup_edge.py, and
# its bytes are checked before the copy gets its name. Nothing here writes the journal.
#
# The default folder is $VINCI_HOME/backups/journal, which holds only these copies. A failure
# exits 1 (a user error, as the other commands do).

from std.python import Python, PythonObject

from embed_edge import edge_module
from session import user_error
from timeutil import Instant, format_instant


def backup_command(
    args: List[String], home: String, journal: String, now: Instant
) raises:
    var directory = home + "/backups/journal"
    var keep = 0
    var i = 0
    while i < len(args):
        var a = args[i]
        if a == "--dir" and i + 1 < len(args):
            directory = args[i + 1].copy()
            i += 2
        elif a == "--keep" and i + 1 < len(args):
            try:
                keep = Int(args[i + 1])
            except:
                raise user_error("--keep must be a whole number")
            if keep < 1:
                raise user_error("--keep must be at least 1")
            i += 2
        else:
            raise user_error(
                "backup takes --dir DIR and --keep N, got '" + a + "'"
            )
    _ = edge_module()
    var edge = Python.import_module("vinci_edge.backup_edge")
    var pair = edge.backup(
        PythonObject(journal),
        PythonObject(directory),
        PythonObject(keep),
        PythonObject(format_instant(now.copy())),
    )
    if String(pair[0]) != "ok":
        raise user_error(String(pair[1]))
    print(String(pair[1]))
