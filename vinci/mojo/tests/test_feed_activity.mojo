# The activity feed (M5 step b): the record rules on fixture pieces, the deny list, the cursor text,
# and the store contract: a second run writes nothing, a changed total is one version, and a read
# that does not cover the whole source retracts nothing. Each test works in a temporary directory.

from std.python import Python
from std.testing import assert_equal, assert_false, assert_true

from feed import apply_read
from feed_activity import (
    Piece,
    aggregate,
    cursor_at,
    denied,
    one_line,
    range_text,
    read_cursor,
    window_title,
)
from feed_store import FeedRead, FeedRecord, feed_record
from index_store import IndexStore, py_params
from timeutil import Instant, parse_instant


def temp_dir() raises -> String:
    var tempfile = Python.import_module("tempfile")
    return String(tempfile.mkdtemp())


def at(text: String) raises -> Instant:
    return parse_instant(text)


def piece(
    start: String, end: String, app: String, title: String
) raises -> Piece:
    return Piece(at(start), at(end), app, title)


def test_title_is_one_line() raises:
    assert_equal(one_line("a\tb\nc\r"), "a b c ")
    assert_equal(
        window_title("Code", "Glider.cs\n- glider"), "Code: Glider.cs - glider"
    )
    assert_equal(window_title("Code", ""), "Code")
    assert_equal(window_title("", "Docs"), "Docs")
    print("test_title_is_one_line: ok")


def test_ranges_merge_within_a_minute() raises:
    var pieces = List[Piece]()
    pieces.append(
        piece(
            "2026-11-18T09:00:00-06:00", "2026-11-18T09:20:00-06:00", "a", "b"
        )
    )
    # Starts 60 s after the last range ends: one range.
    pieces.append(
        piece(
            "2026-11-18T09:21:00-06:00", "2026-11-18T09:30:00-06:00", "a", "b"
        )
    )
    # Starts 61 s after: a new range.
    pieces.append(
        piece(
            "2026-11-18T09:50:01-06:00", "2026-11-18T09:55:00-06:00", "a", "b"
        )
    )
    pieces.append(
        piece(
            "2026-11-18T10:00:00-06:00", "2026-11-18T10:05:00-06:00", "a", "b"
        )
    )
    assert_equal(range_text(pieces), "09:00-09:30, 09:50-09:55, 10:00-10:05")
    print("test_ranges_merge_within_a_minute: ok")


def test_ranges_are_capped() raises:
    var pieces = List[Piece]()
    for k in range(25):
        var start = "2026-11-18T09:" + String(k * 2) + ":00-06:00"
        if k * 2 < 10:
            start = "2026-11-18T09:0" + String(k * 2) + ":00-06:00"
        var end = "2026-11-18T09:0" + String(k * 2) + ":30-06:00"
        if k * 2 >= 10:
            end = "2026-11-18T09:" + String(k * 2) + ":30-06:00"
        pieces.append(piece(start, end, "a", "b"))
    var text = range_text(pieces)
    assert_true(text.endswith(" (+5 more)"))
    assert_equal(len(text.split(", ")), 20)
    print("test_ranges_are_capped: ok")


def test_one_record_per_window_per_day() raises:
    var pieces = List[Piece]()
    # The same window from two buckets on one day: one record, the totals added.
    pieces.append(
        piece(
            "2026-11-18T09:00:00-06:00",
            "2026-11-18T09:10:00-06:00",
            "Code",
            "x",
        )
    )
    pieces.append(
        piece(
            "2026-11-18T09:30:00-06:00",
            "2026-11-18T09:40:00-06:00",
            "Code",
            "x",
        )
    )
    pieces.append(
        piece(
            "2026-11-18T10:00:00-06:00",
            "2026-11-18T10:05:00-06:00",
            "Code",
            "y",
        )
    )
    var records = aggregate(pieces)
    assert_equal(len(records), 2)
    assert_equal(
        records[0].text,
        "Code: x\n2026-11-18: 20m in this window, 09:00-09:10, 09:30-09:40",
    )
    assert_equal(records[0].at, "2026-11-18T09:00:00-06:00")
    assert_equal(records[0].kind, "window")
    assert_equal(records[0].source, "activity")
    assert_false(records[0].trusted)
    print("test_one_record_per_window_per_day: ok")


def test_id_is_stable_and_per_day() raises:
    var day1 = List[Piece]()
    day1.append(
        piece(
            "2026-11-18T09:00:00-06:00",
            "2026-11-18T09:10:00-06:00",
            "Code",
            "x",
        )
    )
    var again = List[Piece]()
    again.append(
        piece(
            "2026-11-18T14:00:00-06:00",
            "2026-11-18T14:05:00-06:00",
            "Code",
            "x",
        )
    )
    var day2 = List[Piece]()
    day2.append(
        piece(
            "2026-11-19T09:00:00-06:00",
            "2026-11-19T09:10:00-06:00",
            "Code",
            "x",
        )
    )
    var a = aggregate(day1)[0].id.copy()
    var b = aggregate(again)[0].id.copy()
    var c = aggregate(day2)[0].id.copy()
    assert_equal(a, b)
    assert_true(a != c)
    assert_true(a.startswith("2026-11-18/"))
    assert_equal(a.byte_length(), String("2026-11-18/").byte_length() + 16)
    print("test_id_is_stable_and_per_day: ok")


def test_deny_matches_folded_text() raises:
    var deny = List[String]()
    deny.append("1PASSWORD")
    deny.append("PRIVATE BROWSING")
    assert_true(denied("1PASSWORD VAULT", deny))
    assert_true(denied("FIREFOX PRIVATE BROWSING", deny))
    assert_false(denied("CODE GLIDER.CS", deny))
    print("test_deny_matches_folded_text: ok")


def test_cursor_round_trip() raises:
    var cursor = read_cursor(
        "aw-watcher-window_a=2026-11-18T15:00:00+00:00;aw-watcher-window_b=2026-11-18T16:00:00Z"
    )
    assert_equal(len(cursor), 2)
    assert_equal(
        cursor_at(cursor, "aw-watcher-window_b"), "2026-11-18T16:00:00Z"
    )
    assert_equal(cursor_at(cursor, "aw-watcher-window_c"), "")
    assert_equal(len(read_cursor("")), 0)
    print("test_cursor_round_trip: ok")


def record(id: String, text: String) raises -> FeedRecord:
    var title = String(text.split("\n")[0])
    return feed_record(
        "activity",
        id,
        "2026-11-18T09:00:00-06:00",
        "window",
        title,
        "",
        text,
        List[String](),
    )


def count_in(store: IndexStore, table: String) raises -> Int:
    var row = store.conn.execute("SELECT COUNT(*) FROM " + table).fetchone()
    return Int(String(row[0]))


def test_store_contract() raises:
    var store = IndexStore(temp_dir() + "/index.sqlite")
    var records = List[FeedRecord]()
    records.append(record("2026-11-18/aaaa", "Code: x\n2026-11-18: 10m"))
    records.append(record("2026-11-18/bbbb", "Docs\n2026-11-18: 5m"))

    var first = FeedRead(
        records.copy(), List[String](), False, "cursor-1", 2, ""
    )
    var r1 = apply_read(
        store, "activity", first, "2026-11-18T10:00:00-06:00", 0.0
    )
    assert_equal(r1.new, 2)
    assert_equal(r1.unchanged, 0)

    # The same records again: nothing is written, and the cursor is the same.
    var again = FeedRead(
        records.copy(), List[String](), False, "cursor-1", 2, ""
    )
    var r2 = apply_read(
        store, "activity", again, "2026-11-18T10:05:00-06:00", 0.0
    )
    assert_equal(r2.new, 0)
    assert_equal(r2.versions, 0)
    assert_equal(r2.unchanged, 2)

    # One total changed: one version, the other is unchanged.
    var changed = List[FeedRecord]()
    changed.append(record("2026-11-18/aaaa", "Code: x\n2026-11-18: 15m"))
    var third = FeedRead(changed^, List[String](), False, "cursor-2", 1, "")
    var r3 = apply_read(
        store, "activity", third, "2026-11-18T10:10:00-06:00", 0.0
    )
    assert_equal(r3.versions, 1)
    assert_equal(r3.unchanged, 0)

    # A read that does not cover the source (complete is False) retracts nothing, even when
    # the record it left out is stored.
    var empty = FeedRead(
        List[FeedRecord](), List[String](), False, "cursor-2", 0, ""
    )
    var r4 = apply_read(
        store, "activity", empty, "2026-11-18T10:15:00-06:00", 0.0
    )
    assert_equal(r4.retracted, 0)

    # The records are in the activity table, and not in the feeds' table.
    assert_equal(count_in(store, "fts_activity"), 3)
    var feeds = store.conn.execute(
        "SELECT COUNT(*) FROM fts_feeds WHERE rowid IN"
        " (SELECT rowid FROM facts WHERE source = 'activity')"
    ).fetchone()
    assert_equal(Int(String(feeds[0])), 0)
    print("test_store_contract: ok")


def main() raises:
    test_title_is_one_line()
    test_ranges_merge_within_a_minute()
    test_ranges_are_capped()
    test_one_record_per_window_per_day()
    test_id_is_stable_and_per_day()
    test_deny_matches_folded_text()
    test_cursor_round_trip()
    test_store_contract()
