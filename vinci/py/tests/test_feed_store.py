"""The feed store (M5 step a): citation ids that are assigned once and never reused, the record id
as a way to open a record, the trusted flag, the feeds table, and deleting one source."""

from pathlib import Path

from vinci_core.feed_store import (
    cite_prefix,
    delete_source_rows,
    feed_cursor,
    feed_record,
    feed_row,
    put_record,
    retract_missing,
    retract_record,
    set_feed,
)
from vinci_core.handles import lookup
from vinci_core.index_store import IndexStore


def record(ident: str, text: str):  # type: ignore[no-untyped-def]
    return feed_record("vaults", ident, "2026-11-18T10:00:00+00:00", "section", "T", "", text, [])


def scalar(store: IndexStore, sql: str, value: str) -> str:
    return str(store.conn.execute(sql, (value,)).fetchone()[0])


def new_store(tmp_path: Path) -> IndexStore:
    return IndexStore(str(tmp_path / "index.sqlite"))


def test_cites_are_kept_and_never_reused(tmp_path: Path) -> None:
    store = new_store(tmp_path)
    store.begin()
    assert put_record(store, record("docs/a.md#one", "first text")) == 1
    assert put_record(store, record("docs/a.md#two", "second text")) == 1
    assert put_record(store, record("docs/a.md#one", "first text, changed")) == 2
    store.commit()
    # Both versions of "one" keep v1, and "two" is v2.
    latest = "SELECT cite FROM facts WHERE record_id = ? ORDER BY rowid DESC LIMIT 1"
    assert scalar(store, latest, "docs/a.md#one") == "v1"
    assert scalar(store, latest, "docs/a.md#two") == "v2"

    store.begin()
    retract_record(store, "vaults", "docs/a.md#two", "2026-11-18T10:05:00+00:00")
    store.commit()
    # A retracted record's number is not given to a new record.
    store.begin()
    assert put_record(store, record("docs/a.md#three", "third")) == 1
    store.commit()
    assert scalar(store, latest, "docs/a.md#three") == "v3"
    assert cite_prefix("git") == "g"


def test_retracted_then_back_is_a_new_version(tmp_path: Path) -> None:
    store = new_store(tmp_path)
    store.begin()
    put_record(store, record("docs/b.md#x", "same text"))
    retract_record(store, "vaults", "docs/b.md#x", "2026-11-18T10:05:00+00:00")
    # The same text again is not "unchanged": the current version is the retraction.
    assert put_record(store, record("docs/b.md#x", "same text")) == 2
    store.commit()
    assert store.fact_total() == 3


def test_open_finds_a_record_by_cite_or_record_id(tmp_path: Path) -> None:
    store = new_store(tmp_path)
    store.begin()
    put_record(store, record("docs/c.md#y", "open me"))
    store.commit()
    by_cite = lookup(store, "v1")
    by_record = lookup(store, "docs/c.md#y")
    assert len(by_cite) == 1
    assert len(by_record) == 1
    assert by_record[0].text == "open me"
    # Feed facts are not trusted, so the chat's write gate sees them.
    assert scalar(store, "SELECT trusted FROM facts WHERE record_id = ?", "docs/c.md#y") == "0"


def test_cursor_row_and_source_deletion(tmp_path: Path) -> None:
    store = new_store(tmp_path)
    assert feed_cursor(store, "git") == ""
    store.begin()
    put_record(store, record("docs/d.md#z", "to delete"))
    set_feed(store, "git", "master:abc", "2026-11-18T10:00:00+00:00", "read 1")
    store.commit()
    assert feed_cursor(store, "git") == "master:abc"
    row = feed_row(store, "git")
    assert row is not None
    assert row[1] == "read 1"

    store.begin()
    delete_source_rows(store, "vaults")
    store.commit()
    assert store.fact_total() == 0
    assert len(lookup(store, "v1")) == 0


def test_retract_missing_keeps_what_is_listed(tmp_path: Path) -> None:
    store = new_store(tmp_path)
    store.begin()
    put_record(store, record("docs/e.md#a", "a"))
    put_record(store, record("docs/e.md#b", "b"))
    put_record(store, record("docs/e.md#c", "c"))
    retracted = retract_missing(store, "vaults", ["docs/e.md#a", "docs/e.md#c"], "2026-11-18T10:10:00+00:00")
    store.commit()
    assert retracted == 1
    status = scalar(
        store,
        "SELECT status FROM facts WHERE record_id = ? AND valid_until IS NULL",
        "docs/e.md#b",
    )
    assert status == "retracted"
    assert lookup(store, "v2")[0].note != ""
