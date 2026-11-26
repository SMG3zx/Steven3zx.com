"""The feed contract's store side (M5 step a, docs/feeds.md). A feed reads an outside source
and gives records: {source, id, at, kind, title, body_path, text, hash, trusted, links}.
Records are stored as facts in the same table as the journal's, keyed by (source, id), and
versioned by hash exactly as journal facts are (IndexStore.put). A record that a feed no
longer sees gets a retraction version, so recall stops returning it and open still shows it.

Citation ids: a feed record is given one id of its own, its source's letter and a number
(g1 for a commit, v1 for a section). The number is assigned once and kept by every later
version. The record id (a short sha, or path#slug) is also accepted by open.

Port of mojo/src/feed_store.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from .index_store import Fact, IndexStore, sha256_hex
from .jsonline import json_string

GIT_SOURCE = "git"
VAULT_SOURCE = "vaults"
ACTIVITY_SOURCE = "activity"
JOURNAL_SOURCE = "journal"
RETRACTED = "retracted"
ACTIVE = "active"


@dataclass
class FeedRecord:
    """One record a feed read. text is the snapshot that is stored, searched and opened: its
    first line is the title, so title is not stored apart from it."""

    source: str
    id: str
    at: str
    kind: str
    title: str
    body_path: str
    text: str
    hash: str
    trusted: bool
    links: list[str]


@dataclass
class FeedRead:
    """What one read of a feed gives. records are the new or changed ones. complete means
    all_ids is every record the source has now, so a stored record missing from it was removed
    at the source and is retracted. cursor is what the next read starts from."""

    records: list[FeedRecord]
    all_ids: list[str]
    complete: bool
    cursor: str
    seen: int
    note: str


def feed_record(
    source: str,
    id: str,
    at: str,
    kind: str,
    title: str,
    body_path: str,
    text: str,
    links: list[str],
) -> FeedRecord:
    """A record with its hash taken from its text. Feed records are never trusted: only the
    journal holds the person's own writing."""
    return FeedRecord(source, id, at, kind, title, body_path, text, sha256_hex(text), False, list(links))


def cite_prefix(source: str) -> str:
    """The letter a source's citation ids start with. Each is unused by the journal
    (i r n s x p h b k m d e t) and by the read and line ids (d, L)."""
    if source == GIT_SOURCE:
        return "g"
    if source == VAULT_SOURCE:
        return "v"
    if source == ACTIVITY_SOURCE:
        return "w"
    raise ValueError("feed: no citation prefix for source " + source)


def next_cite(store: IndexStore, source: str) -> str:
    """The next free citation id of a source: its prefix and one more than the highest number
    any of its facts has, retracted ones included, so a number is never reused."""
    row = store.conn.execute(
        "SELECT COALESCE(MAX(CAST(SUBSTR(cite, 2) AS INTEGER)), 0) FROM facts WHERE source = ?",
        (source,),
    ).fetchone()
    return cite_prefix(source) + str(int(row[0]) + 1)


def put_record(store: IndexStore, rec: FeedRecord) -> int:
    """Store one record under the versioning rules. Returns 0 when its hash is the current one,
    1 for a new record, and 2 for a new version. A record that was retracted and comes back is
    a new version, because its retraction hash never equals a content hash."""
    cite = store.latest_cite(rec.source, rec.id)
    id_cite = next_cite(store, rec.source) if cite is None else cite
    tags: list[str] = []
    if len(rec.body_path) > 0:
        tags.append("body:" + rec.body_path)
    fact = Fact(
        rec.source,
        rec.id,
        rec.hash,
        rec.kind,
        rec.text,
        id_cite,
        rec.at,
        None,
        tags,
        list(rec.links),
        False,
        ACTIVE,
        0,
    )
    return store.put(fact)


def retract_record(store: IndexStore, source: str, id: str, at: str) -> int:
    """End a record's current version with a retraction: the same text under status
    retracted, so recall skips it and open shows it as retracted. Returns 1 when it did, 0
    when there was no active version to retract."""
    row = store.current_fact(source, id)
    if row is None:
        return 0
    if row.status != ACTIVE:
        return 0
    fact = Fact(
        source,
        id,
        RETRACTED + ":" + row.version_hash,
        row.kind,
        row.text,
        row.cite,
        at,
        None,
        [],
        [],
        False,
        RETRACTED,
        0,
    )
    store.put(fact)
    return 1


def retract_missing(store: IndexStore, source: str, keep: list[str], at: str) -> int:
    """Retract every active record of the source whose id is not in keep. Called only when a
    feed has read the whole source, so a missing id means the source no longer has it."""
    kept = set(keep)
    rows = store.conn.execute(
        "SELECT record_id FROM facts WHERE source = ? AND valid_until IS NULL AND status = 'active'"
        " ORDER BY rowid",
        (source,),
    ).fetchall()
    gone = [str(r[0]) for r in rows if str(r[0]) not in kept]
    count = 0
    for ident in gone:
        count += retract_record(store, source, ident, at)
    return count


def delete_source_rows(store: IndexStore, source: str) -> None:
    """Drop every fact of a source, with its search rows, vectors and keys. The caller runs
    this inside its transaction. The handles table is not touched."""
    for stmt in (
        "DELETE FROM vectors WHERE fact IN (SELECT rowid FROM facts WHERE source = ?)",
        "DELETE FROM keys WHERE fact IN (SELECT rowid FROM facts WHERE source = ?)",
        "DELETE FROM fts WHERE rowid IN (SELECT rowid FROM facts WHERE source = ?)",
        "DELETE FROM fts_feeds WHERE rowid IN (SELECT rowid FROM facts WHERE source = ?)",
        "DELETE FROM fts_activity WHERE rowid IN (SELECT rowid FROM facts WHERE source = ?)",
        "DELETE FROM facts WHERE source = ?",
    ):
        store.conn.execute(stmt, (source,))


def feed_cursor(store: IndexStore, name: str) -> str:
    """The stored cursor of a feed, or "" when it has never run."""
    row = store.conn.execute("SELECT cursor FROM feeds WHERE name = ?", (name,)).fetchone()
    if row is None:
        return ""
    return str(row[0])


def set_feed(store: IndexStore, name: str, cursor: str, last_run: str, summary: str) -> None:
    store.conn.execute(
        "INSERT OR REPLACE INTO feeds (name, cursor, last_run, summary) VALUES (?, ?, ?, ?)",
        (name, cursor, last_run, summary),
    )


def feed_row(store: IndexStore, name: str) -> Optional[list[str]]:
    """[last_run, summary] of a feed that has run, else None."""
    row = store.conn.execute("SELECT last_run, summary FROM feeds WHERE name = ?", (name,)).fetchone()
    if row is None:
        return None
    return [str(row[0]), str(row[1])]


def delete_feed_row(store: IndexStore, name: str) -> None:
    store.conn.execute("DELETE FROM feeds WHERE name = ?", (name,))


def count_current(store: IndexStore, source: str) -> int:
    """Active, current versions of a source."""
    row = store.conn.execute(
        "SELECT COUNT(*) FROM facts WHERE source = ? AND valid_until IS NULL AND status = 'active'",
        (source,),
    ).fetchone()
    return int(row[0])


def count_versions(store: IndexStore, source: str) -> int:
    """Every version a source has ever stored, retractions included."""
    row = store.conn.execute("SELECT COUNT(*) FROM facts WHERE source = ?", (source,)).fetchone()
    return int(row[0])


def links_json(links: list[str]) -> str:
    """The links as a JSON array, for a record's line in a report."""
    return "[" + ",".join(json_string(link) for link in links) + "]"
