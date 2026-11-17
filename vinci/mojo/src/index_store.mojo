# The index file: SQLite at $VINCI_HOME/index.sqlite (or --index PATH). It is derived
# data. The journal and loaded records are the store of record, and a rebuild
# from them gives the same rows. Python's sqlite3 is reached through interop at
# the edge: one call per statement.
#
# Schema, version 1:
#   meta(key, value)                 schema_version, journal_offset, journal_lines, last build
#   facts                            one row per version of a fact. The rowid is the fact id.
#                                    cite is the journal-style id ("m12"), line the journal line.
#   keys(fact, key)                  extra search keys per fact, filled by consolidation (step 3)
#   fts (FTS5: text, keys)           rowid = facts.rowid. Every version is indexed; search filters.
#   vectors(fact, label, dims, vec)  embeddings, filled in step 2b
#   handles(id, created, source, text, digest)  text too long for a context, saved under a t id
#                                    (step 3a, handles.mojo). Not a fact: the text is not indexed.
#
# A version is current when valid_until is NULL. Recall also needs status = 'active'.
# The versioning rules are in put(): docs/memory.md states them with the journal's mapping.

from std.python import Python, PythonObject

from jsonline import json_string

comptime SCHEMA_VERSION = "1"


def py_params(*values: PythonObject) raises -> PythonObject:
    """A Python list of sqlite3 parameters, in the order given."""
    var out = Python.list()
    for v in values:
        out.append(v)
    return out


def sha256_hex(text: String) raises -> String:
    """SHA-256 of the UTF-8 bytes, as hex. hashlib keeps the digest stable across runs.
    """
    var hashlib = Python.import_module("hashlib")
    return String(hashlib.sha256(PythonObject(text).encode()).hexdigest())


def json_list(items: List[String]) raises -> String:
    """A JSON array of strings, written without spaces."""
    var out = String("[")
    var first = True
    for item in items:
        if not first:
            out += ","
        out += json_string(item)
        first = False
    return out + "]"


struct Fact(Copyable, Movable):
    """One version of one fact, ready to store. Built by index_build and load.
    """

    var source: String
    var record_id: String
    var version_hash: String
    var kind: String
    var text: String
    var cite: String
    var at: String
    var idea: Optional[String]
    var tags: List[String]
    var refs: List[String]
    var trusted: Bool
    var status: String
    var line: Int

    def __init__(
        out self,
        var source: String,
        var record_id: String,
        var version_hash: String,
        var kind: String,
        var text: String,
        var cite: String,
        var at: String,
        var idea: Optional[String],
        var tags: List[String],
        var refs: List[String],
        trusted: Bool,
        var status: String,
        line: Int,
    ):
        self.source = source^
        self.record_id = record_id^
        self.version_hash = version_hash^
        self.kind = kind^
        self.text = text^
        self.cite = cite^
        self.at = at^
        self.idea = idea^
        self.tags = tags^
        self.refs = refs^
        self.trusted = trusted
        self.status = status^
        self.line = line


@fieldwise_init
struct CurrentVersion(Copyable, Movable):
    var rowid: Int
    var version_hash: String


@fieldwise_init
struct Unembedded(Copyable, Movable):
    """A fact that has no vector yet: its rowid and the text to embed."""

    var rowid: Int
    var text: String


@fieldwise_init
struct KindCount(Copyable, Movable):
    var kind: String
    var total: Int
    var current: Int


struct IndexStore(Movable):
    """An open index file. Callers wrap writes in begin() and commit()."""

    var conn: PythonObject
    var path: String

    def __init__(out self, path: String) raises:
        """Open or create the index at path, and check its schema version."""
        var sqlite3 = Python.import_module("sqlite3")
        self.path = path
        self.conn = sqlite3.connect(PythonObject(path))
        # The schema is one transaction: each separate statement would fsync on its own.
        _ = self.conn.execute("BEGIN IMMEDIATE")
        _ = self.conn.execute(
            "CREATE TABLE IF NOT EXISTS meta ("
            " key TEXT PRIMARY KEY, value TEXT NOT NULL)"
        )
        _ = self.conn.execute(
            "CREATE TABLE IF NOT EXISTS facts ("
            " source TEXT NOT NULL, record_id TEXT NOT NULL,"
            " version_hash TEXT NOT NULL, kind TEXT NOT NULL,"
            " text TEXT NOT NULL, cite TEXT NOT NULL, at TEXT NOT NULL,"
            " valid_from TEXT NOT NULL, valid_until TEXT, idea TEXT,"
            " tags TEXT NOT NULL, refs TEXT NOT NULL,"
            " trusted INTEGER NOT NULL, status TEXT NOT NULL,"
            " line INTEGER NOT NULL)"
        )
        _ = self.conn.execute(
            "CREATE INDEX IF NOT EXISTS facts_record"
            " ON facts (source, record_id)"
        )
        _ = self.conn.execute(
            "CREATE TABLE IF NOT EXISTS keys ("
            " fact INTEGER NOT NULL, key TEXT NOT NULL)"
        )
        _ = self.conn.execute(
            "CREATE VIRTUAL TABLE IF NOT EXISTS fts USING fts5(text, keys)"
        )
        _ = self.conn.execute(
            "CREATE TABLE IF NOT EXISTS vectors ("
            " fact INTEGER NOT NULL, label TEXT NOT NULL,"
            " dims INTEGER NOT NULL, vec BLOB NOT NULL,"
            " PRIMARY KEY (fact, label))"
        )
        # Added in step 3a with IF NOT EXISTS, so a version 1 file opens unchanged.
        _ = self.conn.execute(
            "CREATE TABLE IF NOT EXISTS handles ("
            " id TEXT PRIMARY KEY, created TEXT NOT NULL,"
            " source TEXT NOT NULL, text TEXT NOT NULL, digest TEXT NOT NULL)"
        )
        var version = self.meta_get("schema_version")
        if version == "":
            self.meta_set("schema_version", SCHEMA_VERSION)
        self.conn.commit()
        if version != "" and version != SCHEMA_VERSION:
            raise Error(
                "index: schema "
                + version
                + " is not "
                + SCHEMA_VERSION
                + "; delete the file and run vinci index rebuild"
            )

    def begin(self) raises:
        _ = self.conn.execute("BEGIN IMMEDIATE")

    def commit(self) raises:
        _ = self.conn.execute("COMMIT")

    def rollback(self) raises:
        _ = self.conn.execute("ROLLBACK")

    def meta_get(self, key: String) raises -> String:
        """The stored value, or "" when the key is not set."""
        var row = self.conn.execute(
            "SELECT value FROM meta WHERE key = ?",
            py_params(PythonObject(key)),
        ).fetchone()
        if Bool(row == Python.none()):
            return ""
        return String(row[0])

    def meta_set(self, key: String, value: String) raises:
        _ = self.conn.execute(
            "INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)",
            py_params(PythonObject(key), PythonObject(value)),
        )

    def meta_int(self, key: String) raises -> Int:
        var text = self.meta_get(key)
        if text == "":
            return 0
        return Int(text)

    def has_key(self, source: String, record_id: String) raises -> Bool:
        """True when any version of (source, record_id) was ever stored."""
        var row = self.conn.execute(
            "SELECT 1 FROM facts WHERE source = ? AND record_id = ? LIMIT 1",
            py_params(PythonObject(source), PythonObject(record_id)),
        ).fetchone()
        return not Bool(row == Python.none())

    def current(
        self, source: String, record_id: String
    ) raises -> Optional[CurrentVersion]:
        """The version with no valid_until. Journal order decides it, not dates.
        """
        var row = self.conn.execute(
            (
                "SELECT rowid, version_hash FROM facts"
                " WHERE source = ? AND record_id = ? AND valid_until IS NULL"
                " ORDER BY rowid DESC LIMIT 1"
            ),
            py_params(PythonObject(source), PythonObject(record_id)),
        ).fetchone()
        if Bool(row == Python.none()):
            return None
        return CurrentVersion(Int(String(row[0])), String(row[1]))

    def end_row(self, rowid: Int, at: String) raises:
        """Set valid_until on one version: the at of the line that replaced it.
        """
        _ = self.conn.execute(
            "UPDATE facts SET valid_until = ? WHERE rowid = ?",
            py_params(PythonObject(at), PythonObject(rowid)),
        )

    def end_version(
        self, source: String, record_id: String, at: String
    ) raises -> Int:
        """End the current version of a record, if it has one. Returns how many ended.
        """
        var cur = self.conn.execute(
            (
                "UPDATE facts SET valid_until = ?"
                " WHERE source = ? AND record_id = ? AND valid_until IS NULL"
            ),
            py_params(
                PythonObject(at), PythonObject(source), PythonObject(record_id)
            ),
        )
        return Int(String(cur.rowcount))

    def insert(self, f: Fact) raises -> Int:
        """Store a version as current and index its text. Returns its rowid."""
        var idea = Python.none()
        if f.idea is not None:
            idea = PythonObject(f.idea.value())
        var trusted = 0
        if f.trusted:
            trusted = 1
        var cur = self.conn.execute(
            (
                "INSERT INTO facts (source, record_id, version_hash, kind,"
                " text, cite, at, valid_from, valid_until, idea, tags, refs,"
                " trusted, status, line) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL,"
                " ?, ?, ?, ?, ?, ?)"
            ),
            py_params(
                PythonObject(f.source),
                PythonObject(f.record_id),
                PythonObject(f.version_hash),
                PythonObject(f.kind),
                PythonObject(f.text),
                PythonObject(f.cite),
                PythonObject(f.at),
                PythonObject(f.at),
                idea,
                PythonObject(json_list(f.tags)),
                PythonObject(json_list(f.refs)),
                PythonObject(trusted),
                PythonObject(f.status),
                PythonObject(f.line),
            ),
        )
        var rowid = cur.lastrowid
        _ = self.conn.execute(
            "INSERT INTO fts (rowid, text, keys) VALUES (?, ?, '')",
            py_params(rowid, PythonObject(f.text)),
        )
        return Int(String(rowid))

    def put(mut self, f: Fact) raises -> Int:
        """Store a fact under the versioning rules.

        Returns 0 when nothing changed, 1 for a new record, and 2 for a new version
        that ended an older one. The same latest hash is a no-op. A new hash ends the
        current version at f.at and stores the new one. An older hash is a new version
        too, so A, B, A leaves A current. Older versions are never compared against.
        """
        var cur = self.current(f.source, f.record_id)
        var code = 1
        if cur is not None:
            var old = cur.value().copy()
            if old.version_hash == f.version_hash:
                return 0
            self.end_row(old.rowid, f.at)
            code = 2
        _ = self.insert(f)
        return code

    def kind_counts(self) raises -> List[KindCount]:
        """Versions per kind, with the current and active ones counted apart."""
        var out = List[KindCount]()
        var rows = self.conn.execute(
            "SELECT kind, COUNT(*),"
            " SUM(CASE WHEN valid_until IS NULL AND status = 'active'"
            " THEN 1 ELSE 0 END)"
            " FROM facts GROUP BY kind ORDER BY kind"
        ).fetchall()
        for row in rows:
            out.append(
                KindCount(
                    String(row[0]), Int(String(row[1])), Int(String(row[2]))
                )
            )
        return out^

    def fact_total(self) raises -> Int:
        var row = self.conn.execute("SELECT COUNT(*) FROM facts").fetchone()
        return Int(String(row[0]))

    def file_bytes(self) raises -> Int:
        var os = Python.import_module("os")
        return Int(String(os.path.getsize(PythonObject(self.path))))

    def missing_vectors(self, label: String) raises -> List[Unembedded]:
        """Active facts with no vector for the label, in rowid order.

        Superseded facts still count: the temporal route can reach them, so they need
        vectors too.
        """
        var out = List[Unembedded]()
        var rows = self.conn.execute(
            (
                "SELECT f.rowid, f.text FROM facts AS f WHERE f.status ="
                " 'active' AND NOT EXISTS (SELECT 1 FROM vectors AS v WHERE"
                " v.fact = f.rowid AND v.label = ?) ORDER BY f.rowid"
            ),
            py_params(PythonObject(label)),
        ).fetchall()
        for row in rows:
            out.append(Unembedded(Int(String(row[0])), String(row[1])))
        return out^

    def put_vector(
        self, fact: Int, label: String, blob: PythonObject, dims: Int
    ) raises:
        """Store one packed float32 vector for a fact under a label."""
        _ = self.conn.execute(
            (
                "INSERT OR REPLACE INTO vectors (fact, label, dims, vec)"
                " VALUES (?, ?, ?, ?)"
            ),
            py_params(
                PythonObject(fact),
                PythonObject(label),
                PythonObject(dims),
                blob,
            ),
        )
