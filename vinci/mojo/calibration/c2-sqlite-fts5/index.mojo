from std.python import Python, PythonObject


@fieldwise_init
struct Hit(Copyable, Movable):
    var source: String
    var id: String
    var title: String
    # SQLite bm25(): lower is better, so results come back best first.
    var score: Float64


def params(*values: PythonObject) raises -> PythonObject:
    # sqlite3 takes its parameters as a sequence; build one from Mojo values.
    var out = Python.list()
    for v in values:
        out.append(v)
    return out


def quote_query(query: String) -> String:
    # Each word becomes a quoted FTS5 phrase, so operators and punctuation in
    # user input (quotes, hyphens, NOT, colons, *) are text, not FTS5 syntax.
    var out = String()
    for word in query.split(" "):
        if word.byte_length() == 0:
            continue
        if out.byte_length() > 0:
            out += " "
        out += '"' + String(word).replace('"', '""') + '"'
    return out


struct Index:
    var conn: PythonObject

    def __init__(out self, path: String) raises:
        var sqlite3 = Python.import_module("sqlite3")
        self.conn = sqlite3.connect(PythonObject(path))
        _ = self.conn.execute(
            "CREATE TABLE IF NOT EXISTS records ("
            " seq INTEGER PRIMARY KEY,"
            " source TEXT NOT NULL,"
            " id TEXT NOT NULL,"
            " at TEXT NOT NULL,"
            " kind TEXT NOT NULL,"
            " title TEXT NOT NULL,"
            " body TEXT NOT NULL,"
            " hash TEXT NOT NULL,"
            " UNIQUE (source, id, hash))"
        )
        _ = self.conn.execute(
            "CREATE VIRTUAL TABLE IF NOT EXISTS fts USING fts5(title, body)"
        )
        _ = self.conn.commit()

    def add(
        self,
        source: String,
        id: String,
        at: String,
        kind: String,
        title: String,
        body: String,
        hash: String,
    ) raises:
        # A (source, id, hash) seen before is ignored by UNIQUE, so re-adding
        # changes nothing. A new hash for the same (source, id) is a new row,
        # and search keeps only the newest row per (source, id).
        var cur = self.conn.execute(
            (
                "INSERT OR IGNORE INTO records"
                " (source, id, at, kind, title, body, hash)"
                " VALUES (?, ?, ?, ?, ?, ?, ?)"
            ),
            params(
                PythonObject(source),
                PythonObject(id),
                PythonObject(at),
                PythonObject(kind),
                PythonObject(title),
                PythonObject(body),
                PythonObject(hash),
            ),
        )
        if Bool(cur.rowcount == 1):
            _ = self.conn.execute(
                "INSERT INTO fts (rowid, title, body) VALUES (?, ?, ?)",
                params(cur.lastrowid, PythonObject(title), PythonObject(body)),
            )
        _ = self.conn.commit()

    def search(self, query: String, k: Int) raises -> List[Hit]:
        var hits = List[Hit]()
        var match_expr = quote_query(query)
        if match_expr.byte_length() == 0 or k <= 0:
            return hits^
        var rows = self.conn.execute(
            (
                "SELECT r.source, r.id, r.title, bm25(fts) AS score FROM fts"
                " JOIN records AS r ON r.seq = fts.rowid WHERE fts MATCH ? AND"
                " r.seq IN (SELECT MAX(seq) FROM records GROUP BY source, id)"
                " ORDER BY score LIMIT ?"
            ),
            params(PythonObject(match_expr), PythonObject(k)),
        ).fetchall()
        for row in rows:
            hits.append(
                Hit(
                    String(row[0]),
                    String(row[1]),
                    String(row[2]),
                    Float64(String(row[3])),
                )
            )
        return hits^
