# The Mojo side of the chat's Python edge: vinci_edge.chat_edge (the model call, standard
# input, the vinci child process) and vinci_edge.reading_edge (the read tool's text work).
# Each function is one Python call per request, and returns status words, not exceptions,
# so no Python error text has to be parsed here.

from std.python import Python, PythonObject

from embed_edge import edge_module


@fieldwise_init
struct Status(Copyable, Movable):
    """ok is False when the call failed, and text then says why."""

    var ok: Bool
    var text: String


def chat_edge() raises -> PythonObject:
    """vinci_edge.chat_edge, with mojo/py on sys.path (edge_module puts it there).
    """
    _ = edge_module()
    return Python.import_module("vinci_edge.chat_edge")


def reading_edge() raises -> PythonObject:
    """vinci_edge.reading_edge, with mojo/py on sys.path."""
    _ = edge_module()
    return Python.import_module("vinci_edge.reading_edge")


def complete(
    base_url: String,
    model: String,
    messages_json: String,
    think: Bool,
) raises -> Status:
    """One chat completion. messages_json is a JSON array of role and content objects.
    """
    var edge = chat_edge()
    var pair = edge.complete(
        PythonObject(base_url),
        PythonObject(model),
        PythonObject(messages_json),
        PythonObject(think),
    )
    var ok = String(pair[0]) == "ok"
    return Status(ok, String(pair[1]))


def read_line() raises -> Optional[String]:
    """One line of standard input without its line ending. None at end of input.
    """
    var edge = chat_edge()
    var pair = edge.read_line()
    if String(pair[0]) == "eof":
        return None
    return String(pair[1])


def run_vinci(
    exe: String, args_json: String, no_track: Bool
) raises -> Tuple[Int, String]:
    """Runs the vinci binary exe with a JSON array of arguments. Returns its exit code and
    its output, standard error included, in order. no_track sets VINCI_NO_TRACK=1 for the
    child, so a read-only command from the chat cannot write the tracker's lines.
    """
    var edge = chat_edge()
    var pair = edge.run_vinci(
        PythonObject(exe), PythonObject(args_json), PythonObject(no_track)
    )
    return (Int(String(pair[0])), String(pair[1]))


def read_source(
    target: String,
    is_url: Bool,
    home: String,
    now_iso: String,
) raises -> PythonObject:
    """The read tool's result: a dict with status ok (title, locator, date, count, where)
    or status error (error). Saves the passages as the body file named by where.
    """
    var edge = reading_edge()
    return edge.read_source(
        PythonObject(target),
        PythonObject(is_url),
        PythonObject(home),
        PythonObject(now_iso),
    )
