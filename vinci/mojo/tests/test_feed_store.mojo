# The feed store (M5 step a): citation ids that are assigned once and never reused, the record
# id as a way to open a record, the trusted flag, the feeds table, and deleting one source.

from std.python import Python, PythonObject
from std.testing import assert_equal, assert_false, assert_true

from feed_store import (
    FeedRecord,
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
from handles import lookup
from index_store import IndexStore, py_params


def temp_dir() raises -> String:
    var tempfile = Python.import_module("tempfile")
    return String(tempfile.mkdtemp())


def record(id: String, text: String) raises -> FeedRecord:
    var links = List[String]()
    return feed_record(
        "vaults",
        id,
        "2026-11-18T10:00:00+00:00",
        "section",
        "T",
        "",
        text,
        links,
    )


def scalar(store: IndexStore, sql: String, value: String) raises -> String:
    var row = store.conn.execute(sql, py_params(PythonObject(value))).fetchone()
    return String(row[0])


def test_cites_are_kept_and_never_reused() raises:
    var store = IndexStore(temp_dir() + "/index.sqlite")
    store.begin()
    assert_equal(put_record(store, record("docs/a.md#one", "first text")), 1)
    assert_equal(put_record(store, record("docs/a.md#two", "second text")), 1)
    assert_equal(
        put_record(store, record("docs/a.md#one", "first text, changed")), 2
    )
    store.commit()
    # Both versions of "one" keep v1, and "two" is v2.
    assert_equal(
        scalar(
            store,
            (
                "SELECT cite FROM facts WHERE record_id = ? ORDER BY rowid DESC"
                " LIMIT 1"
            ),
            "docs/a.md#one",
        ),
        "v1",
    )
    assert_equal(
        scalar(
            store,
            (
                "SELECT cite FROM facts WHERE record_id = ? ORDER BY rowid DESC"
                " LIMIT 1"
            ),
            "docs/a.md#two",
        ),
        "v2",
    )

    store.begin()
    _ = retract_record(
        store, "vaults", "docs/a.md#two", "2026-11-18T10:05:00+00:00"
    )
    store.commit()
    # A retracted record's number is not given to a new record.
    store.begin()
    assert_equal(put_record(store, record("docs/a.md#three", "third")), 1)
    store.commit()
    assert_equal(
        scalar(
            store,
            (
                "SELECT cite FROM facts WHERE record_id = ? ORDER BY rowid DESC"
                " LIMIT 1"
            ),
            "docs/a.md#three",
        ),
        "v3",
    )
    assert_equal(cite_prefix("git"), "g")


def test_retracted_then_back_is_a_new_version() raises:
    var store = IndexStore(temp_dir() + "/index.sqlite")
    store.begin()
    _ = put_record(store, record("docs/b.md#x", "same text"))
    _ = retract_record(
        store, "vaults", "docs/b.md#x", "2026-11-18T10:05:00+00:00"
    )
    # The same text again is not "unchanged": the current version is the retraction.
    assert_equal(put_record(store, record("docs/b.md#x", "same text")), 2)
    store.commit()
    assert_equal(store.fact_total(), 3)


def test_open_finds_a_record_by_cite_or_record_id() raises:
    var store = IndexStore(temp_dir() + "/index.sqlite")
    store.begin()
    _ = put_record(store, record("docs/c.md#y", "open me"))
    store.commit()
    var by_cite = lookup(store, "v1")
    var by_record = lookup(store, "docs/c.md#y")
    assert_equal(len(by_cite), 1)
    assert_equal(len(by_record), 1)
    assert_equal(by_record[0].text, "open me")
    # Feed facts are not trusted, so the chat's write gate sees them.
    assert_equal(
        scalar(
            store,
            "SELECT trusted FROM facts WHERE record_id = ?",
            "docs/c.md#y",
        ),
        "0",
    )


def test_cursor_row_and_source_deletion() raises:
    var store = IndexStore(temp_dir() + "/index.sqlite")
    assert_equal(feed_cursor(store, "git"), "")
    store.begin()
    _ = put_record(store, record("docs/d.md#z", "to delete"))
    set_feed(store, "git", "master:abc", "2026-11-18T10:00:00+00:00", "read 1")
    store.commit()
    assert_equal(feed_cursor(store, "git"), "master:abc")
    var row = feed_row(store, "git")
    assert_true(row is not None)
    assert_equal(row.value()[1], "read 1")

    store.begin()
    delete_source_rows(store, "vaults")
    store.commit()
    assert_equal(store.fact_total(), 0)
    var gone = lookup(store, "v1")
    assert_equal(len(gone), 0)


def test_retract_missing_keeps_what_is_listed() raises:
    var store = IndexStore(temp_dir() + "/index.sqlite")
    store.begin()
    _ = put_record(store, record("docs/e.md#a", "a"))
    _ = put_record(store, record("docs/e.md#b", "b"))
    _ = put_record(store, record("docs/e.md#c", "c"))
    var keep = List[String]()
    keep.append("docs/e.md#a")
    keep.append("docs/e.md#c")
    var retracted = retract_missing(
        store, "vaults", keep, "2026-11-18T10:10:00+00:00"
    )
    store.commit()
    assert_equal(retracted, 1)
    assert_equal(
        scalar(
            store,
            (
                "SELECT status FROM facts WHERE record_id = ? AND valid_until"
                " IS NULL"
            ),
            "docs/e.md#b",
        ),
        "retracted",
    )
    assert_false(lookup(store, "v2")[0].note == "")


def main() raises:
    test_cites_are_kept_and_never_reused()
    print("test_cites_are_kept_and_never_reused: ok")
    test_retracted_then_back_is_a_new_version()
    print("test_retracted_then_back_is_a_new_version: ok")
    test_open_finds_a_record_by_cite_or_record_id()
    print("test_open_finds_a_record_by_cite_or_record_id: ok")
    test_cursor_row_and_source_deletion()
    print("test_cursor_row_and_source_deletion: ok")
    test_retract_missing_keeps_what_is_listed()
    print("test_retract_missing_keeps_what_is_listed: ok")
