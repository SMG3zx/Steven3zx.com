"""The command line: settings, clock, routing and dispatch.

Commands live in vinci_core.cmd_ideas, cmd_knowledge and cmd_ops; the shared helpers live
in vinci_core.session.

Exit codes: 0 ok; 1 a user error ("vinci: <message>"); 3 any other failure.

Port of mojo/src/vinci.mojo.
"""

from __future__ import annotations

import os
import sys
import time
from pathlib import Path

from vinci_core.paths import join_path
from vinci_core.backup import backup_command
from vinci_core.chat import run_chat
from vinci_core.cmd_ideas import dispatch_ideas
from vinci_core.cmd_feed import feed_command
from vinci_core.cmd_index import index_command, open_command, recall_command
from vinci_core.cmd_knowledge import dispatch_knowledge
from vinci_core.cmd_ops import dispatch_ops
from vinci_core.portal import portal_command
from vinci_core.command_kinds import command_writes_journal, find_command
from vinci_core.journal import JournalTx
from vinci_core.session import Session, UserError, user_error
from vinci_core.timeutil import DAYS_0001_TO_1970, TICKS_PER_DAY, Instant, parse_instant
from vinci_core.verify import verify_command

# Commands routed before the journal is read. Each is ported in its own module; until it is,
# it fails with a clear message rather than running a partial version.
UNPORTED_ROUTES: tuple[str, ...] = ()


def user_profile() -> str:
    """Environment.GetFolderPath(UserProfile): $HOME on Linux."""
    return os.path.expanduser("~")


def load_env_file(path: str) -> None:
    """KEY=VALUE lines set names that are not set already. A missing file is fine."""
    if not Path(path).exists():
        return
    for raw in Path(path).read_text().split("\n"):
        line = raw.strip()
        if len(line) == 0 or line.startswith("#"):
            continue
        at = line.find("=")
        if at <= 0:
            continue
        name = line[:at].strip()
        value = line[at + 1 :].strip()
        if os.environ.get(name) is None:
            os.environ[name] = value


def local_now() -> Instant:
    """DateTimeOffset.Now: the wall clock with the local offset."""
    unix_ticks = time.time_ns() // 100
    offset_minutes = int(time.localtime().tm_gmtoff) // 60
    return Instant(unix_ticks + DAYS_0001_TO_1970 * TICKS_PER_DAY, offset_minutes)


def parse_now(text: str) -> Instant:
    """The time given in VINCI_NOW. The offset is required, so the same text names one moment
    everywhere."""
    try:
        return parse_instant(text)
    except ValueError:
        raise user_error(
            "VINCI_NOW must be an ISO 8601 time with an offset, got '" + text + "'"
        ) from None


def vinci_home() -> str:
    """$VINCI_HOME, or ~/.vinci when it is not set."""
    home = os.environ.get("VINCI_HOME")
    if home is not None:
        return home
    return join_path(user_profile(), ".vinci")


def journal_path() -> str:
    """$VINCI_HOME (default ~/.vinci) plus journal.jsonl."""
    return join_path(vinci_home(), "journal.jsonl")


def self_path() -> str:
    """The launcher the chat runs for each journal command: VINCI_SELF, else the py/vinci launcher
    beside this package."""
    own = os.environ.get("VINCI_SELF")
    if own:
        return own
    return os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "vinci"))


def run(args: list[str]) -> int:
    """Runs one command. Returns the exit code for commands that set their own (verify)."""
    env_file = os.environ.get("VINCI_ENV_FILE")
    if env_file is not None:
        load_env_file(env_file)
    else:
        load_env_file(join_path(join_path(join_path(user_profile(), ".config"), "vinci"), "memory.env"))

    now_text = os.environ.get("VINCI_NOW")
    now = parse_now(now_text) if now_text is not None else local_now()

    # Routed before any journal is read. `vinci` with no command is chat, whatever the journal
    # holds.
    if len(args) == 0 or args[0] == "chat":
        run_chat(vinci_home(), journal_path(), now, self_path())
        return 0
    if args[0] in UNPORTED_ROUTES:
        raise NotImplementedError(args[0] + " is not ported to Python yet")
    # The index and recall read the journal and write only their own index file.
    if args[0] == "index":
        index_command(args[1:], vinci_home(), journal_path())
        return 0
    if args[0] == "serve":
        portal_command(args[1:], journal_path())
        return 0
    # The feeds read outside sources into the index, and write only the index file.
    if args[0] == "feed":
        feed_command(args[1:], vinci_home(), journal_path(), now)
        return 0
    # Backup and verify read the journal under its lock and never run the tracker.
    if args[0] == "backup":
        backup_command(args[1:], vinci_home(), journal_path(), now)
        return 0
    if args[0] == "verify":
        return verify_command(args[1:], vinci_home(), journal_path(), now)

    cmd = args[0]
    if cmd == "journal":
        # Every journal command, and the two lookups, run under this word. The chat runs the
        # same words through it.
        if len(args) < 2:
            raise user_error("usage: vinci journal <command> [arguments]. Run 'vinci help'.")
        sub = args[1]
        if sub == "recall":
            recall_command(args[2:], vinci_home())
            return 0
        if sub == "open":
            open_command(args[2:], vinci_home())
            return 0
        return run_journal_command(sub, list(args[2:]), now)
    if cmd in ("path", "help"):
        return run_journal_command(cmd, list(args[1:]), now)
    if find_command(cmd) >= 0:
        raise user_error("'" + cmd + "' is a journal command. Run it as 'vinci journal " + cmd + "'.")
    raise user_error("unknown command '" + cmd + "'. Run 'vinci help'.")


def run_journal_command(cmd: str, rest: list[str], now: Instant) -> int:
    """One journal command (command_kinds.py): tracked, locked when it writes, committed once."""
    # An unknown word is refused before anything is read or written.
    if find_command(cmd) < 0:
        raise user_error("unknown command '" + cmd + "'. Run 'vinci help'.")

    # The tracker runs before the command reads the journal (vinci_core.track).
    tracked: list[str] = []
    no_track = os.environ.get("VINCI_NO_TRACK")
    if no_track != "1":
        from vinci_core.track import track_sync, activitywatch_server

        tracked = track_sync(journal_path(), activitywatch_server(), now)

    # Only a command that writes takes the journal's append lock, from its first read to its
    # last append.
    tx = JournalTx(journal_path())
    if command_writes_journal(cmd):
        tx.begin()
    session = Session(tx, now)
    session.tracked = tracked

    try:
        ran = dispatch_ideas(session, cmd, rest)
        if not ran:
            ran = dispatch_knowledge(session, cmd, rest)
        if not ran:
            ran = dispatch_ops(session, cmd, rest)
        if not ran:
            raise user_error("unknown command '" + cmd + "'. Run 'vinci help'.")
        session.commit()
    except BaseException:
        session.abort()
        raise
    return 0


def main(argv: list[str] | None = None) -> int:
    args = sys.argv[1:] if argv is None else argv
    try:
        return run(args)
    except UserError as err:
        print("vinci: " + str(err), file=sys.stderr)
        return 1
    except NotImplementedError as err:
        print("vinci: " + str(err), file=sys.stderr)
        return 3
    except Exception as err:  # a bug or a broken journal
        print("vinci: " + str(err), file=sys.stderr)
        return 3
