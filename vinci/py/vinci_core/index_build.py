"""Builds the index from the journal, and loads external records into it.

Journal lines become facts by the mapping in docs/memory.md: one fact per line that carries
text a person would recall. A read line also gets one fact per chunk of its body
(docs/<sha>.md). One run is one transaction, and the byte offset of the last complete line is
stored with it, so the next run starts there.

Port of mojo/src/index_build.mojo.
"""

from __future__ import annotations

import json
import os
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

from vinci_edge import feed_edge

from .feed_store import delete_source_rows
from .index_store import Fact, IndexStore, sha256_hex
from .journal import Event, parse_event
from .timeutil import format_instant

CHUNK_BYTES = 1000
JOURNAL_SOURCE = "journal"


@dataclass
class BuildReport:
    """What one run did. Counts are for this run, except journal_lines and offset."""

    lines: int = 0
    written: int = 0
    unchanged: int = 0
    ended: int = 0
    bodies: int = 0
    missing_bodies: int = 0
    journal_lines: int = 0
    offset: int = 0
    seconds: float = 0.0
    versions: int = 0
    rebuilt: bool = False


INDEXED_KINDS = frozenset(
    [
        "idea", "for", "goal", "req", "cut", "work", "ship", "retire", "bookmark", "source",
        "note", "home", "issue", "diagnose", "principle", "predict", "result", "commit",
        "missed", "kept", "remember", "memory", "read", "exchange",
    ]
)
VERSIONED_KINDS = frozenset(["for", "goal", "cut", "home", "diagnose", "result", "remember", "memory"])
IDEA_KINDS = frozenset(["idea", "for", "goal", "home", "work", "ship", "retire"])


def indexed_kind(kind: str) -> bool:
    """Kinds whose text a person would recall. Pure state (stage, watch, spent, sync, used,
    review, tend, link, log, spec) is not indexed: it has no text to recall."""
    return kind in INDEXED_KINDS


def versioned_kind(kind: str) -> bool:
    """Kinds whose later line with the same id replaces the earlier one (the fold keeps only
    the last value). Their repeats are new versions. Other kinds are appended events, so a
    repeat of kind and id is a new fact with its own record key."""
    return kind in VERSIONED_KINDS


def memory_kind(kind: str) -> bool:
    return kind in ("remember", "memory")


def idea_kind(kind: str) -> bool:
    """Kinds whose id is an idea id."""
    return kind in IDEA_KINDS


def content_of(e: Event) -> str:
    """The text a recall returns: the line's text, with the part a person searches for: a
    bookmark's why and url, a source's or read's url, a work line's commit."""
    kind = e.kind
    text = e.text or ""
    if kind == "bookmark":
        why = e.why or ""
        if len(why) > 0:
            text += "\n" + why
    if kind == "work":
        # A work line's where is the commit it landed in. People search for commit ids.
        commit = e.where_ or ""
        if len(commit) > 0:
            text += "\ncommit " + commit
    if kind in ("bookmark", "source", "read"):
        url = e.url or ""
        if len(url) > 0:
            text += "\n" + url
    return text


def trusted_of(e: Event) -> bool:
    """The user's own lines are trusted. An assistant's exchange is not the user's writing,
    and read bodies are chunked separately as untrusted."""
    if e.kind == "exchange":
        return (e.who or "") == "user"
    return True


def status_of(e: Event) -> str:
    """A memory line's status tag (archived, retracted, superseded), else active."""
    if not memory_kind(e.kind):
        return "active"
    for tag in e.tags or []:
        if tag in ("retracted", "superseded", "archived"):
            return tag
    return "active"


def first_idea_ref(refs: list[str]) -> Optional[str]:
    """The first ref that is an idea id (ideas are i-prefixed)."""
    for r in refs:
        if r.startswith("i"):
            return r
    return None


def idea_of(e: Event) -> Optional[str]:
    """The idea a line belongs to: its own id for idea kinds, else the first idea ref, else a
    from:<idea id> tag."""
    ident = e.id or ""
    if idea_kind(e.kind) and ident.startswith("i"):
        return ident
    from_refs = first_idea_ref(e.refs or [])
    if from_refs is not None:
        return from_refs
    for tag in e.tags or []:
        if tag.startswith("from:") and len(tag) > 5:
            rest = tag[5:]
            if rest.startswith("i"):
                return rest
    return None


def chunk_body(body: str) -> list[str]:
    """A read body in chunks of about CHUNK_BYTES bytes, cut at line boundaries.

    A markdown heading always starts a new chunk, so headings act as sections. A line longer
    than CHUNK_BYTES is kept whole. Blank-only chunks are dropped.
    """
    chunks: list[str] = []
    cur = ""
    for line in body.split("\n"):
        heading = line.startswith("#")
        if len(cur.encode("utf-8")) > 0 and (
            heading or len(cur.encode("utf-8")) + len(line.encode("utf-8")) + 1 > CHUNK_BYTES
        ):
            if len(cur.strip()) > 0:
                chunks.append(cur)
            cur = ""
        if len(cur.encode("utf-8")) > 0:
            cur += "\n"
        cur += line
    if len(cur.strip()) > 0:
        chunks.append(cur)
    return chunks


def join_path(directory: str, name: str) -> str:
    if directory.endswith("/"):
        return directory + name
    return directory + "/" + name


def index_event(store: IndexStore, e: Event, line: int, line_hash: str, home: str, report: BuildReport) -> None:
    """Store one journal line as facts, applying its corrections first."""
    kind = e.kind
    if not indexed_kind(kind):
        return
    text = content_of(e)
    if len(text) == 0 and kind != "read":
        return
    at = format_instant(e.at)
    ident = e.id or ""
    refs = list(e.refs or [])

    # A memory line's refs name the memories it supersedes: k ids are remember records, m ids
    # are memory records. The superseded version ends at this line.
    if memory_kind(kind):
        for target in refs:
            if target.startswith("k"):
                report.ended += store.end_version(JOURNAL_SOURCE, "remember:" + target, at)
            elif target.startswith("m"):
                report.ended += store.end_version(JOURNAL_SOURCE, "memory:" + target, at)

    # Record key: kind and id, so an idea and a work line with the same id stay apart. A repeat
    # of an appended kind gets its line number in its key.
    if len(ident) == 0:
        record_id = kind + ":L" + str(line)
        cite = "L" + str(line)
    else:
        cite = ident
        bare = kind + ":" + ident
        if versioned_kind(kind) or not store.has_key(JOURNAL_SOURCE, bare):
            record_id = bare
        else:
            record_id = bare + "#L" + str(line)

    fact = Fact(
        JOURNAL_SOURCE,
        record_id,
        line_hash,
        kind,
        text,
        cite,
        at,
        idea_of(e),
        list(e.tags or []),
        refs,
        trusted_of(e),
        status_of(e),
        line,
    )
    code = store.put(fact)
    if code == 0:
        report.unchanged += 1
    else:
        report.written += 1
        if code == 2:
            report.ended += 1
            report.versions += 1

    if kind == "read":
        index_read_body(store, e, record_id, cite, at, line, home, report)


def index_read_body(
    store: IndexStore,
    e: Event,
    title_record: str,
    title_cite: str,
    at: str,
    line: int,
    home: str,
    report: BuildReport,
) -> None:
    """One untrusted fact per chunk of the read line's body."""
    where_ = e.where_ or ""
    if len(where_) == 0:
        report.missing_bodies += 1
        return
    path = join_path(home, where_)
    if not Path(path).exists():
        report.missing_bodies += 1
        return
    report.bodies += 1
    for n, chunk in enumerate(chunk_body(Path(path).read_text(encoding="utf-8")), start=1):
        number = str(n)
        fact = Fact(
            JOURNAL_SOURCE,
            title_record + "/" + number,
            sha256_hex(chunk),
            "read",
            chunk,
            title_cite + "/" + number,
            at,
            None,
            [],
            [title_cite],
            False,
            "active",
            line,
        )
        code = store.put(fact)
        if code == 0:
            report.unchanged += 1
        else:
            report.written += 1
            if code == 2:
                report.ended += 1
                report.versions += 1


def strip_cr(line: str) -> str:
    """A line without its trailing carriage return, if it has one."""
    if line.endswith("\r"):
        return line[:-1]
    return line


def file_bytes(path: str) -> int:
    if not Path(path).exists():
        return 0
    return os.path.getsize(path)


def update_from_journal(store: IndexStore, journal: str, home: str) -> BuildReport:
    """Index the journal lines after the stored offset. A full rebuild is an empty store.

    The journal's cursor is its byte offset and the sha-256 of the bytes before it. When those
    bytes changed (the journal was rewritten), the journal's facts are dropped and it is read
    again from the start, and the report says so. A journal shorter than the offset is refused:
    it was truncated, which is for the person to decide (vinci feed rebuild).

    Only complete lines are read: a last line without its newline is left for the next run, in
    case another process is still writing it.
    """
    report = BuildReport()
    started = time.perf_counter()
    offset = store.meta_int("journal_offset")
    lines_before = store.meta_int("journal_lines")
    if file_bytes(journal) < offset:
        raise ValueError("index: the journal is shorter than the index's offset; run vinci feed rebuild journal")
    status, prefix, text_after = feed_edge.journal_split(journal, offset)
    if status == "error":
        raise ValueError("index: cannot read the journal: " + prefix)
    split_status, split_prefix, split_text = status, prefix, text_after
    stored = store.meta_get("journal_prefix")

    store.begin()
    try:
        if offset > 0 and stored == "":
            # An index made before the prefix was kept: this journal's prefix is the reference.
            store.meta_set("journal_prefix", split_prefix)
            stored = split_prefix
        if offset > 0 and stored != split_prefix:
            delete_source_rows(store, JOURNAL_SOURCE)
            report.rebuilt = True
            offset = 0
            lines_before = 0
            store.meta_set("journal_offset", "0")
            store.meta_set("journal_lines", "0")
            store.meta_set("journal_prefix", "")
            split_status, split_prefix, split_text = feed_edge.journal_split(journal, 0)
        text = split_text if split_status == "ok" else ""
        parts = text.split("\n")
        consumed = 0
        lineno = lines_before
        for i, piece in enumerate(parts):
            if i == len(parts) - 1:
                break  # the tail after the last newline: incomplete or empty
            consumed += len(piece.encode("utf-8")) + 1
            lineno += 1
            report.lines += 1
            line = strip_cr(piece)
            if len(line) == 0:
                continue
            try:
                e = parse_event(line)
                index_event(store, e, lineno, sha256_hex(line), home, report)
            except Exception as err:
                raise ValueError("index: journal line " + str(lineno) + ": " + str(err)) from None
        report.offset = offset + consumed
        report.journal_lines = lineno
        seconds = time.perf_counter() - started
        report.seconds = seconds
        # Nothing new means nothing to write: an update with no new lines costs no fsync.
        if consumed > 0:
            store.meta_set("journal_offset", str(report.offset))
            store.meta_set("journal_lines", str(lineno))
            store.meta_set("last_build_seconds", str(seconds))
            store.meta_set("journal_prefix", feed_edge.journal_prefix(journal, report.offset)[1])
    except BaseException as err:
        store.rollback()
        raise err
    store.commit()
    return report


def load_records(store: IndexStore, path: str) -> BuildReport:
    """Load external records, one JSON object per line.

    Each object has source, id, at, kind, text, hash, trusted and links (a list of ids it
    refers to). The hash is the record's version: a missing one is the SHA-256 of the text.
    Same versioning rules as the journal, keyed by (source, id).
    """
    report = BuildReport()
    started = time.perf_counter()
    text = Path(path).read_text(encoding="utf-8") if Path(path).exists() else ""
    parts = text.split("\n")
    store.begin()
    try:
        for i, raw in enumerate(parts):
            line = raw.strip()
            if len(line) == 0:
                continue
            report.lines += 1
            obj = json.loads(line)
            source = str(obj["source"])
            ident = str(obj["id"])
            kind = str(obj["kind"])
            body = str(obj["text"])
            hash_value = obj.get("hash")
            version_hash = sha256_hex(body) if hash_value is None else str(hash_value)
            # A missing trusted field is untrusted: only the user's own writing is trusted.
            trusted = str(obj.get("trusted")) == "True"
            links = obj.get("links")
            refs = [] if links is None else [str(link) for link in links]
            fact = Fact(
                source,
                ident,
                version_hash,
                kind,
                body,
                ident,
                str(obj["at"]),
                first_idea_ref(refs),
                [],
                refs,
                trusted,
                "active",
                i + 1,
            )
            code = store.put(fact)
            if code == 0:
                report.unchanged += 1
            else:
                report.written += 1
                if code == 2:
                    report.ended += 1
    except BaseException as err:
        store.rollback()
        raise ValueError("index load: " + str(err)) from None
    report.seconds = time.perf_counter() - started
    store.commit()
    return report
