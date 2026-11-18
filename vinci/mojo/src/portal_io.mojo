# The Mojo side of the portal's Python edge (mojo/py/vinci_edge/portal_edge.py): the calls
# that touch sockets, processes and the file system's text decoding. Each wrapper is one
# call into Python. Nothing here decides what a page says.
#
# Mojo 1.2 has no way for Python to call Mojo, so the process is Mojo and the HTTP layer is
# Python's http.server, reached through this file (as embed_edge.mojo reaches its edge).

from std.python import Python, PythonObject
from std.sys import argv


def env_or(name: String, fallback: String) raises -> String:
    var os = Python.import_module("os")
    var value = os.environ.get(PythonObject(name), Python.none())
    if Bool(value == Python.none()):
        return fallback
    return String(value)


def edge() raises -> PythonObject:
    """vinci_edge.portal_edge, with mojo/py on sys.path. VINCI_MOJO_PY names that directory;
    otherwise it is ../py from the binary (mojo/build/vinci-portal -> mojo/py).
    """
    var sys = Python.import_module("sys")
    var os = Python.import_module("os")
    var py_dir = env_or("VINCI_MOJO_PY", "")
    if py_dir.byte_length() == 0:
        var argv0 = String(argv()[0])
        var dirname = os.path.dirname(PythonObject(argv0))
        py_dir = String(
            os.path.join(dirname, PythonObject(".."), PythonObject("py"))
        )
    var resolved = String(os.path.abspath(PythonObject(py_dir)))
    var present = False
    for entry in sys.path:
        if String(entry) == resolved:
            present = True
    if not present:
        sys.path.insert(0, PythonObject(resolved))
    return Python.import_module("vinci_edge.portal_edge")


def lower_invariant(text: String) raises -> String:
    """String.ToLowerInvariant (the edge holds the .NET differences)."""
    return String(edge().lower_invariant(PythonObject(text)))


def clock() raises -> Float64:
    """Seconds on a monotonic clock."""
    return Float64(String(edge().clock()))


def absolute_path(path: String) raises -> String:
    return String(edge().absolute_path(PythonObject(path)))


def read_text_lenient(path: String) raises -> String:
    return String(edge().read_text_lenient(PythonObject(path)))


def git_portal_files(repository: String) raises -> String:
    return String(edge().git_portal_files(PythonObject(repository)))


def start_capture(
    url: String, draft: String, final: String, chromium: String
) raises:
    _ = edge().start_capture(
        PythonObject(url),
        PythonObject(draft),
        PythonObject(final),
        PythonObject(chromium),
    )


def listen(port: Int) raises -> PythonObject:
    """Bind the loopback addresses. Raises when the port is taken."""
    return edge().listen(PythonObject(port))


def next_request(listener: PythonObject) raises -> PythonObject:
    return edge().next_request(listener)


def start_probe(ports: List[Int]) raises -> PythonObject:
    var py_ports = Python.list()
    for port in ports:
        py_ports.append(PythonObject(port))
    return edge().start_probe(py_ports)
