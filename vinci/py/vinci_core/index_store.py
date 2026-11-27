"""The index file: SQLite at $VINCI_HOME/index.sqlite (or --index PATH). It is derived data.
The journal and loaded records are the store of record, and a rebuild from them gives the
same rows.

Schema, version 1:
  meta(key, value)                 schema_version, journal_offset, journal_lines, last build
  facts                            one row per version of a fact. The rowid is the fact id.
                                   cite is the journal-style id ("m12"), line the journal line.
  keys(fact, key)                  extra search keys per fact, filled by consolidation (step 3)
  fts (FTS5: text, keys)           rowid = facts.rowid. Every version is indexed; search filters.
                                   Journal facts only.
  fts_feeds (FTS5: text, keys)     the same for feed records (git, vaults).
  fts_activity (FTS5: text, keys)  the same for the activity feed's window records.
  vectors(fact, label, dims, vec)  embeddings, filled by `vinci index embed`
  handles(id, created, source, text, digest)  text too long for a context, saved under a t id.
  feeds(name, cursor, last_run, summary)  one row per feed.

A version is current when valid_until is NULL. Recall also needs status = 'active'. The
versioning rules are in put(): docs/memory.md states them with the journal's mapping.

Port of mojo/src/index_store.mojo. Python's sqlite3 is used directly (the Mojo version went
through interop, one call per statement).
"""

from __future__ import annotations

import hashlib
import os
import sqlite3
from dataclasses import dataclass
from typing import Optional

from .jsonline import json_string

SCHEMA_VERSION = "1"

# Matches the facts that a citation id names: a journal id (cite), or a feed record's record id
# (the short sha of a commit, or the path and slug of a section).
CITE_MATCH = "(cite = ? OR (source <> 'journal' AND record_id = ?))"


def json_list(items: list[str]) -> str:
    """A JSON array of strings, written without spaces."""
    return "[" + ",".join(json_string(item) for item in items) + "]"


def sha256_hex(text: str) -> str:
    """SHA-256 of the UTF-8 bytes, as hex."""
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


@dataclass
class Fact:
    """One version of one fact, ready to store. Built by index_build and load."""

    source: str
    record_id: str
    version_hash: str
    kind: str
    text: str
    cite: str
    at: str
    idea: Optional[str]
    tags: list[str]
    refs: list[str]
    trusted: bool
    status: str
    line: int


@dataclass
class CurrentVersion:
    rowid: int
    version_hash: str


@dataclass
class FactRow:
    """The current version of a record, with what a retraction needs to copy."""

    rowid: int
    version_hash: str
    kind: str
    text: str
    cite: str
    status: str


@dataclass
class Unembedded:
    """A fact that has no vector yet: its rowid and the text to embed."""

    rowid: int
    text: str


@dataclass
class KindCount:
    kind: str
    total: int
    current: int


class IndexStore:
    """An open index file. Callers wrap writes in begin() and commit()."""

    def __init__(self, path: str) -> None:
        """Open or create the index at path, and check its schema version."""
        self.path: str = path
        if path != ":memory:":
            os.makedirs(os.path.dirname(os.path.abspath(path)), exist_ok=True)
        self.conn: sqlite3.Connection = sqlite3.connect(path)
        # The schema is one transaction: each separate statement would fsync on its own.
        self.conn.execute("BEGIN IMMEDIATE")
        self.conn.execute(
            "CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)"
        )
        self.conn.execute(
            "CREATE TABLE IF NOT EXISTS facts ("
            " source TEXT NOT NULL, record_id TEXT NOT NULL,"
            " version_hash TEXT NOT NULL, kind TEXT NOT NULL,"
            " text TEXT NOT NULL, cite TEXT NOT NULL, at TEXT NOT NULL,"
            " valid_from TEXT NOT NULL, valid_until TEXT, idea TEXT,"
            " tags TEXT NOT NULL, refs TEXT NOT NULL,"
            " trusted INTEGER NOT NULL, status TEXT NOT NULL,"
            " line INTEGER NOT NULL)"
        )
        self.conn.execute("CREATE INDEX IF NOT EXISTS facts_record ON facts (source, record_id)")
        self.conn.execute("CREATE TABLE IF NOT EXISTS keys (fact INTEGER NOT NULL, key TEXT NOT NULL)")
        self.conn.execute("CREATE VIRTUAL TABLE IF NOT EXISTS fts USING fts5(text, keys)")
        # The feeds' search table. An index that has feed rows in fts moves them here once,
        # when this table is first created.
        feeds_table = self.table_exists("fts_feeds")
        self.conn.execute("CREATE VIRTUAL TABLE IF NOT EXISTS fts_feeds USING fts5(text, keys)")
        if not feeds_table:
            self.conn.execute(
                "INSERT INTO fts_feeds (rowid, text, keys) SELECT fts.rowid, fts.text, fts.keys"
                " FROM fts JOIN facts AS f ON f.rowid = fts.rowid WHERE f.source <> 'journal'"
            )
            self.conn.execute(
                "DELETE FROM fts WHERE rowid IN (SELECT rowid FROM facts WHERE source <> 'journal')"
            )
        # The activity feed has its own table, so loading its windows does not move the
        # bm25 scores of the journal or of git and vaults.
        self.conn.execute("CREATE VIRTUAL TABLE IF NOT EXISTS fts_activity USING fts5(text, keys)")
        self.conn.execute(
            "CREATE TABLE IF NOT EXISTS vectors ("
            " fact INTEGER NOT NULL, label TEXT NOT NULL,"
            " dims INTEGER NOT NULL, vec BLOB NOT NULL,"
            " PRIMARY KEY (fact, label))"
        )
        self.conn.execute(
            "CREATE TABLE IF NOT EXISTS handles ("
            " id TEXT PRIMARY KEY, created TEXT NOT NULL,"
            " source TEXT NOT NULL, text TEXT NOT NULL, digest TEXT NOT NULL)"
        )
        self.conn.execute(
            "CREATE TABLE IF NOT EXISTS feeds ("
            " name TEXT PRIMARY KEY, cursor TEXT NOT NULL,"
            " last_run TEXT NOT NULL, summary TEXT NOT NULL)"
        )
        version = self.meta_get("schema_version")
        if version == "":
            self.meta_set("schema_version", SCHEMA_VERSION)
        self.conn.commit()
        if version != "" and version != SCHEMA_VERSION:
            raise ValueError(
                "index: schema "
                + version
                + " is not "
                + SCHEMA_VERSION
                + "; delete the file and run vinci index rebuild"
            )

    def table_exists(self, name: str) -> bool:
        row = self.conn.execute(
            "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?", (name,)
        ).fetchone()
        return row is not None

    def begin(self) -> None:
        self.conn.execute("BEGIN IMMEDIATE")

    def commit(self) -> None:
        self.conn.execute("COMMIT")

    def rollback(self) -> None:
        self.conn.execute("ROLLBACK")

    def meta_get(self, key: str) -> str:
        """The stored value, or "" when the key is not set."""
        row = self.conn.execute("SELECT value FROM meta WHERE key = ?", (key,)).fetchone()
        if row is None:
            return ""
        return str(row[0])

    def meta_set(self, key: str, value: str) -> None:
        self.conn.execute("INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)", (key, value))

    def meta_int(self, key: str) -> int:
        text = self.meta_get(key)
        if text == "":
            return 0
        return int(text)

    def has_key(self, source: str, record_id: str) -> bool:
        """True when any version of (source, record_id) was ever stored."""
        row = self.conn.execute(
            "SELECT 1 FROM facts WHERE source = ? AND record_id = ? LIMIT 1", (source, record_id)
        ).fetchone()
        return row is not None

    def current(self, source: str, record_id: str) -> Optional[CurrentVersion]:
        """The version with no valid_until. Journal order decides it, not dates."""
        row = self.conn.execute(
            "SELECT rowid, version_hash FROM facts"
            " WHERE source = ? AND record_id = ? AND valid_until IS NULL"
            " ORDER BY rowid DESC LIMIT 1",
            (source, record_id),
        ).fetchone()
        if row is None:
            return None
        return CurrentVersion(int(row[0]), str(row[1]))

    def current_fact(self, source: str, record_id: str) -> Optional[FactRow]:
        """The current version as a whole row, for a retraction to copy from."""
        row = self.conn.execute(
            "SELECT rowid, version_hash, kind, text, cite, status FROM facts"
            " WHERE source = ? AND record_id = ? AND valid_until IS NULL"
            " ORDER BY rowid DESC LIMIT 1",
            (source, record_id),
        ).fetchone()
        if row is None:
            return None
        return FactRow(int(row[0]), str(row[1]), str(row[2]), str(row[3]), str(row[4]), str(row[5]))

    def latest_cite(self, source: str, record_id: str) -> Optional[str]:
        """The citation id a record was given when it was first stored. Every version of a
        record keeps it, so a citation stays valid across versions."""
        row = self.conn.execute(
            "SELECT cite FROM facts WHERE source = ? AND record_id = ? ORDER BY rowid DESC LIMIT 1",
            (source, record_id),
        ).fetchone()
        if row is None:
            return None
        return str(row[0])

    def end_row(self, rowid: int, at: str) -> None:
        """Set valid_until on one version: the at of the line that replaced it."""
        self.conn.execute("UPDATE facts SET valid_until = ? WHERE rowid = ?", (at, rowid))

    def end_version(self, source: str, record_id: str, at: str) -> int:
        """End the current version of a record, if it has one. Returns how many ended."""
        cur = self.conn.execute(
            "UPDATE facts SET valid_until = ?"
            " WHERE source = ? AND record_id = ? AND valid_until IS NULL",
            (at, source, record_id),
        )
        return cur.rowcount

    def insert(self, f: Fact) -> int:
        """Store a version as current and index its text. Returns its rowid."""
        cur = self.conn.execute(
            "INSERT INTO facts (source, record_id, version_hash, kind,"
            " text, cite, at, valid_from, valid_until, idea, tags, refs,"
            " trusted, status, line) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?)",
            (
                f.source,
                f.record_id,
                f.version_hash,
                f.kind,
                f.text,
                f.cite,
                f.at,
                f.at,
                f.idea,
                json_list(f.tags),
                json_list(f.refs),
                1 if f.trusted else 0,
                f.status,
                f.line,
            ),
        )
        rowid = cur.lastrowid
        table = "fts"
        if f.source == "activity":
            table = "fts_activity"
        elif f.source != "journal":
            table = "fts_feeds"
        self.conn.execute(
            "INSERT INTO " + table + " (rowid, text, keys) VALUES (?, ?, '')", (rowid, f.text)
        )
        return int(rowid)

    def put(self, f: Fact) -> int:
        """Store a fact under the versioning rules.

        Returns 0 when nothing changed, 1 for a new record, and 2 for a new version that ended
        an older one. The same latest hash is a no-op. A new hash ends the current version at
        f.at and stores the new one. An older hash is a new version too, so A, B, A leaves A
        current. Older versions are never compared against.
        """
        cur = self.current(f.source, f.record_id)
        code = 1
        if cur is not None:
            if cur.version_hash == f.version_hash:
                return 0
            self.end_row(cur.rowid, f.at)
            code = 2
        self.insert(f)
        return code

    def kind_counts(self) -> list[KindCount]:
        """Versions per kind, with the current and active ones counted apart."""
        rows = self.conn.execute(
            "SELECT kind, COUNT(*),"
            " SUM(CASE WHEN valid_until IS NULL AND status = 'active' THEN 1 ELSE 0 END)"
            " FROM facts GROUP BY kind ORDER BY kind"
        ).fetchall()
        return [KindCount(str(r[0]), int(r[1]), int(r[2] or 0)) for r in rows]

    def fact_total(self) -> int:
        row = self.conn.execute("SELECT COUNT(*) FROM facts").fetchone()
        return int(row[0])

    def file_bytes(self) -> int:
        return os.path.getsize(self.path)

    def missing_vectors(self, label: str) -> list[Unembedded]:
        """Active facts with no vector for the label, in rowid order.

        Superseded facts still count: the temporal route can reach them, so they need vectors
        too.
        """
        rows = self.conn.execute(
            "SELECT f.rowid, f.text FROM facts AS f WHERE f.status = 'active' AND NOT EXISTS"
            " (SELECT 1 FROM vectors AS v WHERE v.fact = f.rowid AND v.label = ?)"
            " ORDER BY f.rowid",
            (label,),
        ).fetchall()
        return [Unembedded(int(r[0]), str(r[1])) for r in rows]

    def put_vector(self, fact: int, label: str, blob: bytes, dims: int) -> None:
        """Store one packed float32 vector for a fact under a label."""
        self.conn.execute(
            "INSERT OR REPLACE INTO vectors (fact, label, dims, vec) VALUES (?, ?, ?, ?)",
            (fact, label, dims, blob),
        )
