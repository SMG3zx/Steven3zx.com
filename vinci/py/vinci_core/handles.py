"""Handles and the ids that `vinci open` reads.

A handle is text too long to put into a context whole. It is saved under an id with a short
digest, and `open` reads part of it later. Handle ids are "t" ids (t1, t2 ...). No journal id
uses t, and save_handle also skips any id that a fact already has as its cite, so a handle
never shares an id with a fact.

A fact is not copied into the handles table. Its text is already in facts, and its citation
id is its id, so open reads the facts table for it. A read document (a `d` fact) is opened as
its title followed by its body: the chunks stored under dN/1, dN/2 ...

Port of mojo/src/handles.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from .index_store import CITE_MATCH, IndexStore

HANDLE_PREFIX = "t"
DIGEST_CHARS = 100
ELLIPSIS = "…"
SUPERSEDED = "superseded"


def date_of(at: str) -> str:
    """The calendar date at the start of a journal time (yyyy-mm-dd)."""
    return at[:10]


def collapsed_text(text: str) -> str:
    """The text with each run of whitespace as one space, and no leading or trailing space."""
    out: list[str] = []
    pending = False
    for ch in text:
        if ch in " \t\n\r":
            pending = True
            continue
        if pending and len(out) > 0:
            out.append(" ")
        pending = False
        out.append(ch)
    return "".join(out)


def digest_is_cut(text: str) -> bool:
    """True when digest_of leaves part of text out, so an open would show more."""
    return len(collapsed_text(text)) > DIGEST_CHARS


def digest_of(text: str) -> str:
    """A short digest for a pointer line: the first DIGEST_CHARS characters, with whitespace
    collapsed, and an ellipsis after them when the text is longer. Characters are code points."""
    cps = collapsed_text(text)
    if len(cps) <= DIGEST_CHARS:
        return cps
    return cps[:DIGEST_CHARS] + ELLIPSIS


@dataclass
class Stored:
    """One thing `open` can read: a handle, or one current version of a fact.

    note is "" for a current version. Otherwise it says why the version is not current
    (superseded, retracted, archived), and only the latest version of an id is given.
    """

    kind: str
    at: str
    text: str
    note: str


def cite_used(store: IndexStore, ident: str) -> bool:
    row = store.conn.execute("SELECT 1 FROM facts WHERE cite = ? LIMIT 1", (ident,)).fetchone()
    return row is not None


def next_handle_id(store: IndexStore) -> str:
    """The next free t id. It skips ids that a handle or a fact already has."""
    row = store.conn.execute(
        "SELECT COALESCE(MAX(CAST(SUBSTR(id, 2) AS INTEGER)), 0) FROM handles"
    ).fetchone()
    n = int(row[0]) + 1
    while cite_used(store, HANDLE_PREFIX + str(n)):
        n += 1
    return HANDLE_PREFIX + str(n)


def save_handle(store: IndexStore, source: str, created: str, text: str) -> str:
    """Save text as a handle and return its id. It commits on its own, so it must not run
    inside another transaction. source names what the text came from (a tool call, a read
    document, a journal id); created is the journal time it was saved at."""
    store.begin()
    try:
        ident = next_handle_id(store)
        store.conn.execute(
            "INSERT INTO handles (id, created, source, text, digest) VALUES (?, ?, ?, ?, ?)",
            (ident, created, source, text, digest_of(text)),
        )
        store.commit()
        return ident
    except BaseException as err:
        store.rollback()
        raise ValueError(str(err)) from None


def handle_of(store: IndexStore, ident: str) -> Optional[Stored]:
    row = store.conn.execute("SELECT created, text FROM handles WHERE id = ?", (ident,)).fetchone()
    if row is None:
        return None
    return Stored("handle", str(row[0]), str(row[1]), "")


def body_of(store: IndexStore, cite: str) -> str:
    """The current chunks of a read document, in order, joined by blank lines. Empty when the
    document has no body in the index."""
    rows = store.conn.execute(
        "SELECT text FROM facts WHERE kind = 'read' AND valid_until IS NULL"
        " AND status = 'active' AND substr(cite, 1, ?) = ? ORDER BY rowid",
        (len(cite.encode("utf-8")) + 1, cite + "/"),
    ).fetchall()
    out = ""
    for row in rows:
        if len(out) > 0:
            out += "\n\n"
        out += str(row[0])
    return out


def facts_of(store: IndexStore, ident: str) -> list[Stored]:
    """Every current version of the facts with this cite, in journal order. When none is
    current, the latest version, with the reason it is not."""
    rows = store.conn.execute(
        "SELECT kind, text, at, valid_until, status FROM facts WHERE "
        + CITE_MATCH
        + " ORDER BY rowid",
        (ident, ident),
    ).fetchall()
    current: list[Stored] = []
    latest: Optional[Stored] = None
    for row in rows:
        kind = str(row[0])
        text = str(row[1])
        at = str(row[2])
        ended = row[3] is not None
        status = str(row[4])
        if not ended and status == "active":
            body = body_of(store, ident) if kind == "read" else ""
            if len(body) > 0:
                text += "\n\n" + body
            current.append(Stored(kind, at, text, ""))
        else:
            note = SUPERSEDED if status == "active" else status
            latest = Stored(kind, at, text, note)
    if len(current) > 0:
        return current
    if latest is not None:
        return [latest]
    return []


def lookup(store: IndexStore, ident: str) -> list[Stored]:
    """Everything `open` can read under id: its handle (if it is a handle id) and its facts.
    An id can name more than one fact, for example an idea and its work lines. Nothing is
    hidden: an empty list means the id is unknown."""
    out: list[Stored] = []
    handle = handle_of(store, ident)
    if handle is not None:
        out.append(handle)
    out.extend(facts_of(store, ident))
    return out
