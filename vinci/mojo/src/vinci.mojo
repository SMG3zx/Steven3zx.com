# vinci: the Mojo port of the vinci command line. The reference is the C# binary:
# src/Program.cs (settings, clock, routing) and src/App.cs (Dispatch).
# Commands live in cmd_ideas.mojo, cmd_knowledge.mojo and cmd_ops.mojo; the
# shared helpers live in session.mojo.
#
# Exit codes: 0 ok; 1 a user error ("vinci: <message>", as App.Run);
# 2 a command the Mojo port does not have yet; 3 any other failure.

from std.pathlib import Path
from std.python import Python, PythonObject
from std.sys import argv, exit

from chat import run_chat
from cmd_feed import feed_command
from cmd_index import index_command, open_command, recall_command
from cmd_ideas import dispatch_ideas
from cmd_knowledge import dispatch_knowledge
from cmd_ops import dispatch_ops
from journal import Journal
from portal import portal_command
from session import (
    NOT_PORTED_PREFIX,
    USER_PREFIX,
    Session,
    user_error,
)
from text import ascii_tail, truncate_div
from timeutil import (
    DAYS_0001_TO_1970,
    TICKS_PER_DAY,
    Instant,
    ascii_prefix,
    parse_instant,
)


def env_value(name: String) raises -> Optional[String]:
    """Environment.GetEnvironmentVariable: None when unset, else the value, even when empty.
    """
    var os = Python.import_module("os")
    var value = os.environ.get(PythonObject(name))
    if Bool(value == Python.none()):
        return None
    return String(value)


def join_path(dir: String, name: String) -> String:
    """Path.Combine for two parts on Linux."""
    if dir.byte_length() == 0:
        return name
    if dir.endswith("/"):
        return dir + name
    return dir + "/" + name


def user_profile() raises -> String:
    """Environment.GetFolderPath(UserProfile): $HOME on Linux."""
    var os = Python.import_module("os")
    return String(os.path.expanduser("~"))


def index_of_byte(s: String, want: Int) -> Int:
    """The byte index of the first want byte, or -1."""
    var b = s.as_bytes()
    for i in range(len(b)):
        if Int(b[i]) == want:
            return i
    return -1


def load_env_file(path: String) raises:
    """Env.LoadFile: KEY=VALUE lines set names that are not set already. Missing file is fine.
    """
    if not Path(path).exists():
        return
    var os = Python.import_module("os")
    for raw in Path(path).read_text().split("\n"):
        var line = String(String(raw).strip())
        if line.byte_length() == 0 or line.startswith("#"):
            continue
        var at = index_of_byte(line, 61)  # '='
        if at <= 0:
            continue
        var name = String(ascii_prefix(line, at).strip())
        var value = String(ascii_tail(line, at + 1).strip())
        if Bool(os.environ.get(PythonObject(name)) == Python.none()):
            os.environ[PythonObject(name)] = PythonObject(value)


def local_now() raises -> Instant:
    """DateTimeOffset.Now: the wall clock with the local offset (time.localtime).
    """
    var time = Python.import_module("time")
    var unix_ticks = Int(String(time.time_ns())) // 100
    var offset_minutes = truncate_div(
        Int(String(time.localtime().tm_gmtoff)), 60
    )
    return Instant(
        unix_ticks + DAYS_0001_TO_1970 * TICKS_PER_DAY, offset_minutes
    )


def parse_now(text: String) raises -> Instant:
    """App.TryParseNow. The offset is required, so the same text names one moment everywhere.
    """
    try:
        return parse_instant(text)
    except:
        raise user_error(
            "VINCI_NOW must be an ISO 8601 time with an offset, got '"
            + text
            + "'"
        )


def vinci_home() raises -> String:
    """Program.cs: $VINCI_HOME, or ~/.vinci when it is not set."""
    var home = env_value("VINCI_HOME")
    if home is not None:
        return home.value()
    return join_path(user_profile(), ".vinci")


def journal_path() raises -> String:
    """Program.cs: $VINCI_HOME (default ~/.vinci) plus journal.jsonl."""
    return join_path(vinci_home(), "journal.jsonl")


def self_path() raises -> String:
    """The absolute path of this binary, which the chat runs for each journal command.
    """
    var os = Python.import_module("os")
    return String(os.path.abspath(PythonObject(String(argv()[0]))))


def run() raises:
    var env_file = env_value("VINCI_ENV_FILE")
    if env_file is not None:
        load_env_file(env_file.value())
    else:
        load_env_file(
            join_path(
                join_path(join_path(user_profile(), ".config"), "vinci"),
                "memory.env",
            )
        )

    var now: Instant
    var now_text = env_value("VINCI_NOW")
    if now_text is not None:
        now = parse_now(now_text.value())
    else:
        now = local_now()

    # VINCI_NO_TRACK is accepted and ignored: this port never reads the watchers or git.
    var argv_all = argv()
    var args = List[String]()
    for i in range(1, len(argv_all)):
        args.append(String(argv_all[i]))

    # Program.cs routes these before any journal is read. `vinci` with no
    # command is chat in C#, whatever the journal holds.
    if len(args) == 0 or args[0] == "chat":
        run_chat(vinci_home(), journal_path(), now.copy(), self_path())
        return
    if args[0] == "serve":
        var serve_args = List[String]()
        for i in range(1, len(args)):
            serve_args.append(args[i])
        portal_command(serve_args, journal_path())
        return
    # The index and recall read the journal and write only their own index file.
    if args[0] == "index":
        var index_args = List[String]()
        for i in range(1, len(args)):
            index_args.append(args[i])
        index_command(index_args, vinci_home(), journal_path())
        return
    # The feeds read outside sources into the index (M5), and write only the index file.
    if args[0] == "feed":
        var feed_args = List[String]()
        for i in range(1, len(args)):
            feed_args.append(args[i])
        feed_command(feed_args, vinci_home(), journal_path(), now.copy())
        return
    if args[0] == "recall":
        var recall_args = List[String]()
        for i in range(1, len(args)):
            recall_args.append(args[i])
        recall_command(recall_args, vinci_home())
        return
    if args[0] == "open":
        var open_args = List[String]()
        for i in range(1, len(args)):
            open_args.append(args[i])
        open_command(open_args, vinci_home())
        return
    if args[0] == "memory":
        # Mark 2 removed the memory service (Neo4j, the C# store and its commands).
        raise user_error(
            "memory was removed in mark 2 (the Neo4j memory service is gone). "
            "Its notes are in the journal; see docs/memory.md."
        )

    var session = Session(Journal(journal_path()), now^)
    var cmd = args[0]
    var rest = List[String]()
    for i in range(1, len(args)):
        rest.append(args[i])

    if dispatch_ideas(session, cmd, rest):
        return
    if dispatch_knowledge(session, cmd, rest):
        return
    if dispatch_ops(session, cmd, rest):
        return
    raise user_error("unknown command '" + cmd + "'. Run 'vinci help'.")


def stderr_line(text: String):
    """Write one line to standard error. Nothing to do if Python cannot write it.
    """
    try:
        var sys = Python.import_module("sys")
        sys.stderr.write(text + "\n")
    except:
        pass


def report_and_exit(message: String):
    """Print the error the way App.Run and the Mojo markers describe, then exit with its code.
    """
    if message.startswith(USER_PREFIX):
        stderr_line("vinci: " + ascii_tail(message, 5))
        exit(1)
    if message.startswith(NOT_PORTED_PREFIX):
        stderr_line(
            "vinci: " + ascii_tail(message, 10) + " is not in the Mojo port yet"
        )
        exit(2)
    stderr_line("vinci: " + message)
    exit(3)


def main():
    try:
        run()
    except e:
        report_and_exit(String(e))
