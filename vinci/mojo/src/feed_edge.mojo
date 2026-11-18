# The Mojo side of the feeds' Python edge (py/vinci_edge/feed_edge.py): git, the Markdown
# walk, feeds.toml and the journal's bytes. Each call returns a status word and a value, and
# no Python exception reaches Mojo. Mojo never walks a folder or runs git itself.

from std.python import Python, PythonObject

from embed_edge import edge_module


@fieldwise_init
struct Reply(Movable):
    """status is "ok", "error" or "missing". value is the edge's result when ok.
    """

    var status: String
    var value: PythonObject


@fieldwise_init
struct Split(Movable):
    """The journal's sha-256 of its first offset bytes, and the text after them.
    """

    var status: String
    var prefix: String
    var text: String


def feed_edge() raises -> PythonObject:
    """vinci_edge.feed_edge, with mojo/py on sys.path (edge_module puts it there).
    """
    _ = edge_module()
    return Python.import_module("vinci_edge.feed_edge")


def py_strings(items: List[String]) raises -> PythonObject:
    """A Python list of strings, in order."""
    var out = Python.list()
    for item in items:
        out.append(PythonObject(item))
    return out


def git_head(repo: String, branch: String) raises -> Reply:
    """The full sha of a branch's tip. value is the sha when status is ok."""
    var pair = feed_edge().git_head(PythonObject(repo), PythonObject(branch))
    return Reply(String(pair[0]), pair[1])


def git_is_ancestor(
    repo: String, ancestor: String, head: String
) raises -> Reply:
    """value is "yes" or "no" when status is ok."""
    var pair = feed_edge().git_is_ancestor(
        PythonObject(repo), PythonObject(ancestor), PythonObject(head)
    )
    return Reply(String(pair[0]), pair[1])


def git_commits(repo: String, rev: String) raises -> Reply:
    """The first-parent commits of rev, newest first, as a list of dicts (see the edge).
    """
    var pair = feed_edge().git_commits(PythonObject(repo), PythonObject(rev))
    return Reply(String(pair[0]), pair[1])


def list_markdown(
    roots: List[String], exclude: List[String], max_bytes: Int
) raises -> Reply:
    """{files, skipped} for the Markdown files under the roots (see the edge).
    """
    var pair = feed_edge().list_markdown(
        py_strings(roots), py_strings(exclude), PythonObject(max_bytes)
    )
    return Reply(String(pair[0]), pair[1])


def read_text(path: String) raises -> Reply:
    """The file as text. value is the text when status is ok."""
    var pair = feed_edge().read_text(PythonObject(path))
    return Reply(String(pair[0]), pair[1])


def journal_split(path: String, offset: Int) raises -> Split:
    """The journal's prefix hash and the text from offset on. A missing file is status
    "missing" with no text."""
    var triple = feed_edge().journal_split(
        PythonObject(path), PythonObject(offset)
    )
    return Split(String(triple[0]), String(triple[1]), String(triple[2]))


def journal_prefix(path: String, length: Int) raises -> String:
    """The sha-256 of the journal's first length bytes."""
    var pair = feed_edge().journal_prefix(
        PythonObject(path), PythonObject(length)
    )
    if String(pair[0]) != "ok":
        raise Error("journal: cannot read " + path + ": " + String(pair[1]))
    return String(pair[1])


def feed_config(path: String) raises -> Reply:
    """The settings from feeds.toml with the defaults filled in, as a dict."""
    var pair = feed_edge().feed_config(PythonObject(path))
    return Reply(String(pair[0]), pair[1])


def activity_buckets(server: String) raises -> Reply:
    """The window buckets and their afk twins: value is {"pairs", "skipped"} when ok.
    """
    var pair = feed_edge().activity_buckets(PythonObject(server))
    return Reply(String(pair[0]), pair[1])


def activity_pieces(
    server: String,
    window_id: String,
    afk_id: String,
    from_text: String,
    to_text: String,
) raises -> Reply:
    """One bucket pair's window events in a period: value is the list of rows when ok.
    """
    var pair = feed_edge().activity_pieces(
        PythonObject(server),
        PythonObject(window_id),
        PythonObject(afk_id),
        PythonObject(from_text),
        PythonObject(to_text),
    )
    return Reply(String(pair[0]), pair[1])
