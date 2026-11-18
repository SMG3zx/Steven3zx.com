# The feed contract's store side (M5 step a, docs/feeds.md). A feed reads an outside source
# and gives records: {source, id, at, kind, title, body_path, text, hash, trusted, links}.
# Records are stored as facts in the same table as the journal's, keyed by (source, id), and
# versioned by hash exactly as journal facts are (IndexStore.put). A record that a feed no
# longer sees gets a retraction version, so recall stops returning it and open still shows it.
#
# Citation ids: a feed record is given one id of its own, its source's letter and a number
# (g1 for a commit, v1 for a section). The number is assigned once and kept by every later
# version. The record id (a short sha, or path#slug) is also accepted by open.

from std.python import Python, PythonObject

from index_store import CITE_MATCH, Fact, IndexStore, py_params, sha256_hex
from jsonline import json_string

comptime GIT_SOURCE = "git"
comptime VAULT_SOURCE = "vaults"
comptime JOURNAL_SOURCE = "journal"
comptime RETRACTED = "retracted"
comptime ACTIVE = "active"


@fieldwise_init
struct FeedRecord(Copyable, Movable):
    """One record a feed read. text is the snapshot that is stored, searched and opened:
    its first line is the title, so title is not stored apart from it."""

    var source: String
    var id: String
    var at: String
    var kind: String
    var title: String
    var body_path: String
    var text: String
    var hash: String
    var trusted: Bool
    var links: List[String]


@fieldwise_init
struct FeedRead(Movable):
    """What one read of a feed gives. records are the new or changed ones. complete means
    all_ids is every record the source has now, so a stored record missing from it was
    removed at the source and is retracted. cursor is what the next read starts from.
    """

    var records: List[FeedRecord]
    var all_ids: List[String]
    var complete: Bool
    var cursor: String
    var seen: Int
    var note: String


def feed_record(
    source: String,
    id: String,
    at: String,
    kind: String,
    title: String,
    body_path: String,
    var text: String,
    links: List[String],
) raises -> FeedRecord:
    """A record with its hash taken from its text. Feed records are never trusted: only the
    journal holds the person's own writing."""
    var digest = sha256_hex(text)
    return FeedRecord(
        source,
        id,
        at,
        kind,
        title,
        body_path,
        text^,
        digest,
        False,
        links.copy(),
    )


def cite_prefix(source: String) raises -> String:
    """The letter a source's citation ids start with. Each is unused by the journal
    (i r n s x p h b k m d e t) and by the read and line ids (d, L)."""
    if source == GIT_SOURCE:
        return "g"
    if source == VAULT_SOURCE:
        return "v"
    raise Error("feed: no citation prefix for source " + source)


def next_cite(store: IndexStore, source: String) raises -> String:
    """The next free citation id of a source: its prefix and one more than the highest
    number any of its facts has, retracted ones included, so a number is never reused.
    """
    var row = store.conn.execute(
        (
            "SELECT COALESCE(MAX(CAST(SUBSTR(cite, 2) AS INTEGER)), 0)"
            " FROM facts WHERE source = ?"
        ),
        py_params(PythonObject(source)),
    ).fetchone()
    return cite_prefix(source) + String(Int(String(row[0])) + 1)


def put_record(mut store: IndexStore, rec: FeedRecord) raises -> Int:
    """Store one record under the versioning rules. Returns 0 when its hash is the current
    one, 1 for a new record, and 2 for a new version. A record that was retracted and comes
    back is a new version, because its retraction hash never equals a content hash.
    """
    var cite = store.latest_cite(rec.source, rec.id)
    var id_cite: String
    if cite is None:
        id_cite = next_cite(store, rec.source)
    else:
        id_cite = cite.value()
    var tags = List[String]()
    if rec.body_path.byte_length() > 0:
        tags.append("body:" + rec.body_path)
    var fact = Fact(
        rec.source,
        rec.id,
        rec.hash,
        rec.kind,
        rec.text,
        id_cite,
        rec.at,
        None,
        tags^,
        rec.links.copy(),
        False,
        ACTIVE,
        0,
    )
    return store.put(fact)


def retract_record(
    mut store: IndexStore, source: String, id: String, at: String
) raises -> Int:
    """End a record's current version with a retraction: the same text under status
    retracted, so recall skips it and open shows it as retracted. Returns 1 when it did,
    0 when there was no active version to retract."""
    var cur = store.current_fact(source, id)
    if cur is None:
        return 0
    var row = cur.value().copy()
    if row.status != ACTIVE:
        return 0
    var fact = Fact(
        source,
        id,
        RETRACTED + ":" + row.version_hash,
        row.kind,
        row.text,
        row.cite,
        at,
        None,
        List[String](),
        List[String](),
        False,
        RETRACTED,
        0,
    )
    _ = store.put(fact)
    return 1


def retract_missing(
    mut store: IndexStore, source: String, keep: List[String], at: String
) raises -> Int:
    """Retract every active record of the source whose id is not in keep. Called only when
    a feed has read the whole source, so a missing id means the source no longer has it.
    """
    var kept = keep_set(keep)
    var rows = store.conn.execute(
        (
            "SELECT record_id FROM facts WHERE source = ? AND valid_until IS"
            " NULL AND status = 'active' ORDER BY rowid"
        ),
        py_params(PythonObject(source)),
    ).fetchall()
    var gone = List[String]()
    for row in rows:
        var id = String(row[0])
        if id not in kept:
            gone.append(id)
    var count = 0
    for id in gone:
        count += retract_record(store, source, id, at)
    return count


def keep_set(ids: List[String]) raises -> Dict[String, Bool]:
    """The ids as a set, for membership tests."""
    var out = Dict[String, Bool]()
    for id in ids:
        out[id] = True
    return out^


def delete_source_rows(mut store: IndexStore, source: String) raises:
    """Drop every fact of a source, with its search rows, vectors and keys. The caller
    runs this inside its transaction. The handles table is not touched."""
    _ = store.conn.execute(
        (
            "DELETE FROM vectors WHERE fact IN"
            " (SELECT rowid FROM facts WHERE source = ?)"
        ),
        py_params(PythonObject(source)),
    )
    _ = store.conn.execute(
        (
            "DELETE FROM keys WHERE fact IN"
            " (SELECT rowid FROM facts WHERE source = ?)"
        ),
        py_params(PythonObject(source)),
    )
    _ = store.conn.execute(
        (
            "DELETE FROM fts WHERE rowid IN"
            " (SELECT rowid FROM facts WHERE source = ?)"
        ),
        py_params(PythonObject(source)),
    )
    _ = store.conn.execute(
        (
            "DELETE FROM fts_feeds WHERE rowid IN"
            " (SELECT rowid FROM facts WHERE source = ?)"
        ),
        py_params(PythonObject(source)),
    )
    _ = store.conn.execute(
        "DELETE FROM facts WHERE source = ?", py_params(PythonObject(source))
    )


def feed_cursor(store: IndexStore, name: String) raises -> String:
    """The stored cursor of a feed, or "" when it has never run."""
    var row = store.conn.execute(
        "SELECT cursor FROM feeds WHERE name = ?", py_params(PythonObject(name))
    ).fetchone()
    if Bool(row == Python.none()):
        return ""
    return String(row[0])


def set_feed(
    mut store: IndexStore,
    name: String,
    cursor: String,
    last_run: String,
    summary: String,
) raises:
    _ = store.conn.execute(
        (
            "INSERT OR REPLACE INTO feeds (name, cursor, last_run, summary)"
            " VALUES (?, ?, ?, ?)"
        ),
        py_params(
            PythonObject(name),
            PythonObject(cursor),
            PythonObject(last_run),
            PythonObject(summary),
        ),
    )


def feed_row(store: IndexStore, name: String) raises -> Optional[List[String]]:
    """[last_run, summary] of a feed that has run, else None."""
    var row = store.conn.execute(
        "SELECT last_run, summary FROM feeds WHERE name = ?",
        py_params(PythonObject(name)),
    ).fetchone()
    if Bool(row == Python.none()):
        return None
    var out = List[String]()
    out.append(String(row[0]))
    out.append(String(row[1]))
    return Optional[List[String]](out^)


def delete_feed_row(mut store: IndexStore, name: String) raises:
    _ = store.conn.execute(
        "DELETE FROM feeds WHERE name = ?", py_params(PythonObject(name))
    )


def count_current(store: IndexStore, source: String) raises -> Int:
    """Active, current versions of a source."""
    var row = store.conn.execute(
        (
            "SELECT COUNT(*) FROM facts WHERE source = ? AND valid_until IS"
            " NULL AND status = 'active'"
        ),
        py_params(PythonObject(source)),
    ).fetchone()
    return Int(String(row[0]))


def count_versions(store: IndexStore, source: String) raises -> Int:
    """Every version a source has ever stored, retractions included."""
    var row = store.conn.execute(
        "SELECT COUNT(*) FROM facts WHERE source = ?",
        py_params(PythonObject(source)),
    ).fetchone()
    return Int(String(row[0]))


def links_json(links: List[String]) raises -> String:
    """The links as a JSON array, for a record's line in a report."""
    var out = String("[")
    var first = True
    for link in links:
        if not first:
            out += ","
        out += json_string(link)
        first = False
    return out + "]"
