# Handles and the ids that `vinci open` reads (M3 step 3a, the one-journal plan, phase 3).
#
# A handle is text too long to put into a context whole. It is saved under an id with a
# short digest, and `open` reads part of it later. Handle ids are "t" ids (t1, t2 ...).
# No journal id uses t (the journal prefixes are i r n s x p h b, and the import adds
# k m d e), and save_handle also skips any id that a fact already has as its cite, so a
# handle never shares an id with a fact.
#
# A fact is not copied into the handles table. Its text is already in facts, and its
# citation id is its id, so open reads the facts table for it. A read document (a `d`
# fact) is opened as its title followed by its body: the chunks stored under dN/1, dN/2 ...

from std.python import Python, PythonObject

from index_store import CITE_MATCH, IndexStore, py_params
from text import code_points_to_string, utf8_code_points

comptime HANDLE_PREFIX = "t"
comptime DIGEST_CHARS = 100
comptime ELLIPSIS = 0x2026
comptime SUPERSEDED = "superseded"


def date_of(at: String) -> String:
    """The calendar date at the start of a journal time (yyyy-mm-dd)."""
    if at.byte_length() < 10:
        return at.copy()
    var b = at.as_bytes()
    var out = List[UInt8]()
    for i in range(10):
        out.append(b[i])
    return String(unsafe_from_utf8=out)


def is_space_point(cp: Int) -> Bool:
    return cp == 32 or cp == 9 or cp == 10 or cp == 13


def collapsed_points(text: String) raises -> List[Int]:
    """The code points of text with each run of whitespace as one space, and no
    leading or trailing space."""
    var out = List[Int]()
    var pending = False
    var points = utf8_code_points(text)
    for cp in points:
        if is_space_point(cp):
            pending = True
            continue
        if pending and len(out) > 0:
            out.append(32)
        pending = False
        out.append(cp)
    return out^


def digest_is_cut(text: String) raises -> Bool:
    """True when digest_of leaves part of text out, so an open would show more.
    """
    var cps = collapsed_points(text)
    return len(cps) > DIGEST_CHARS


def digest_of(text: String) raises -> String:
    """A short digest for a pointer line: the first DIGEST_CHARS characters, with
    whitespace collapsed, and "…" after them when the text is longer. Characters are
    Unicode code points, not UTF-16 units."""
    var cps = collapsed_points(text)
    if len(cps) <= DIGEST_CHARS:
        return code_points_to_string(cps)
    var kept = List[Int]()
    for i in range(DIGEST_CHARS):
        kept.append(cps[i])
    kept.append(ELLIPSIS)
    return code_points_to_string(kept)


@fieldwise_init
struct Stored(Copyable, Movable):
    """One thing `open` can read: a handle, or one current version of a fact.

    note is "" for a current version. Otherwise it says why the version is not current
    (superseded, retracted, archived), and only the latest version of an id is given.
    """

    var kind: String
    var at: String
    var text: String
    var note: String


def cite_used(store: IndexStore, id: String) raises -> Bool:
    var row = store.conn.execute(
        "SELECT 1 FROM facts WHERE cite = ? LIMIT 1",
        py_params(PythonObject(id)),
    ).fetchone()
    return not Bool(row == Python.none())


def next_handle_id(store: IndexStore) raises -> String:
    """The next free t id. It skips ids that a handle or a fact already has."""
    var row = store.conn.execute(
        "SELECT COALESCE(MAX(CAST(SUBSTR(id, 2) AS INTEGER)), 0) FROM handles"
    ).fetchone()
    var n = Int(String(row[0])) + 1
    while cite_used(store, HANDLE_PREFIX + String(n)):
        n += 1
    return HANDLE_PREFIX + String(n)


def save_handle(
    mut store: IndexStore,
    source: String,
    created: String,
    text: String,
) raises -> String:
    """Save text as a handle and return its id. It commits on its own, so it must not
    run inside another transaction. source names what the text came from (a tool call,
    a read document, a journal id); created is the journal time it was saved at.
    """
    store.begin()
    try:
        var id = next_handle_id(store)
        _ = store.conn.execute(
            (
                "INSERT INTO handles (id, created, source, text, digest)"
                " VALUES (?, ?, ?, ?, ?)"
            ),
            py_params(
                PythonObject(id),
                PythonObject(created),
                PythonObject(source),
                PythonObject(text),
                PythonObject(digest_of(text)),
            ),
        )
        store.commit()
        return id
    except err:
        store.rollback()
        raise Error(String(err))


def handle_of(store: IndexStore, id: String) raises -> Optional[Stored]:
    var row = store.conn.execute(
        "SELECT created, text FROM handles WHERE id = ?",
        py_params(PythonObject(id)),
    ).fetchone()
    if Bool(row == Python.none()):
        return None
    return Optional[Stored](
        Stored("handle", String(row[0]), String(row[1]), "")
    )


def body_of(store: IndexStore, cite: String) raises -> String:
    """The current chunks of a read document, in order, joined by blank lines. Empty when
    the document has no body in the index."""
    var rows = store.conn.execute(
        (
            "SELECT text FROM facts WHERE kind = 'read' AND valid_until IS NULL"
            " AND status = 'active' AND substr(cite, 1, ?) = ? ORDER BY rowid"
        ),
        py_params(
            PythonObject(cite.byte_length() + 1), PythonObject(cite + "/")
        ),
    ).fetchall()
    var out = String()
    for row in rows:
        if out.byte_length() > 0:
            out += "\n\n"
        out += String(row[0])
    return out^


def facts_of(store: IndexStore, id: String) raises -> List[Stored]:
    """Every current version of the facts with this cite, in journal order. When none is
    current, the latest version, with the reason it is not."""
    var out = List[Stored]()
    var rows = store.conn.execute(
        (
            "SELECT kind, text, at, valid_until, status FROM facts WHERE "
            + CITE_MATCH
            + " ORDER BY rowid"
        ),
        py_params(PythonObject(id), PythonObject(id)),
    ).fetchall()
    var current = List[Stored]()
    var latest = Optional[Stored](None)
    for row in rows:
        var kind = String(row[0])
        var text = String(row[1])
        var at = String(row[2])
        var ended = Bool(row[3] != Python.none())
        var status = String(row[4])
        if not ended and status == "active":
            var body = String()
            if kind == "read":
                body = body_of(store, id)
            if body.byte_length() > 0:
                text += "\n\n" + body
            current.append(Stored(kind, at, text, ""))
        else:
            var note = status
            if status == "active":
                note = SUPERSEDED
            latest = Optional[Stored](Stored(kind, at, text, note))
    if len(current) > 0:
        return current^
    if latest is not None:
        out.append(latest.value().copy())
    return out^


def lookup(store: IndexStore, id: String) raises -> List[Stored]:
    """Everything `open` can read under id: its handle (if it is a handle id) and its
    facts. An id can name more than one fact, for example an idea and its work lines.
    Nothing is hidden: an empty list means the id is unknown."""
    var out = List[Stored]()
    var handle = handle_of(store, id)
    if handle is not None:
        out.append(handle.value().copy())
    for s in facts_of(store, id):
        out.append(s.copy())
    return out^
