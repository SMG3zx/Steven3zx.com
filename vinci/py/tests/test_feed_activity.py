"""The activity feed (M5 step b): the record rules on fixture pieces, the deny list, the cursor text,
and the store contract: a second run writes nothing, a changed total is one version, and a read that
does not cover the whole source retracts nothing. Each test works in a temporary directory."""

from pathlib import Path

from vinci_core.feed import apply_read
from vinci_core.feed_activity import Piece, aggregate, cursor_at, denied, one_line, range_text, read_cursor, window_title
from vinci_core.feed_store import FeedRead, FeedRecord, feed_record
from vinci_core.index_store import IndexStore
from vinci_core.timeutil import parse_instant


def piece(start: str, end: str, app: str, title: str) -> Piece:
    return Piece(parse_instant(start), parse_instant(end), app, title)


def test_title_is_one_line() -> None:
    assert one_line("a\tb\nc\r") == "a b c "
    assert window_title("Code", "Glider.cs\n- glider") == "Code: Glider.cs - glider"
    assert window_title("Code", "") == "Code"
    assert window_title("", "Docs") == "Docs"


def test_ranges_merge_within_a_minute() -> None:
    pieces = [
        piece("2026-11-18T09:00:00-06:00", "2026-11-18T09:20:00-06:00", "a", "b"),
        # Starts 60 s after the last range ends: one range.
        piece("2026-11-18T09:21:00-06:00", "2026-11-18T09:30:00-06:00", "a", "b"),
        # Starts 61 s after: a new range.
        piece("2026-11-18T09:50:01-06:00", "2026-11-18T09:55:00-06:00", "a", "b"),
        piece("2026-11-18T10:00:00-06:00", "2026-11-18T10:05:00-06:00", "a", "b"),
    ]
    assert range_text(pieces) == "09:00-09:30, 09:50-09:55, 10:00-10:05"


def test_ranges_are_capped() -> None:
    pieces = []
    for k in range(25):
        minutes = f"{k * 2:02d}"
        pieces.append(piece(f"2026-11-18T09:{minutes}:00-06:00", f"2026-11-18T09:{minutes}:30-06:00", "a", "b"))
    text = range_text(pieces)
    assert text.endswith(" (+5 more)")
    assert len(text.split(", ")) == 20


def test_one_record_per_window_per_day() -> None:
    pieces = [
        piece("2026-11-18T09:00:00-06:00", "2026-11-18T09:10:00-06:00", "Code", "x"),
        piece("2026-11-18T09:30:00-06:00", "2026-11-18T09:40:00-06:00", "Code", "x"),
        piece("2026-11-18T10:00:00-06:00", "2026-11-18T10:05:00-06:00", "Code", "y"),
    ]
    records = aggregate(pieces)
    assert len(records) == 2
    assert records[0].text == "Code: x\n2026-11-18: 20m in this window, 09:00-09:10, 09:30-09:40"
    assert records[0].at == "2026-11-18T09:00:00-06:00"
    assert records[0].kind == "window"
    assert records[0].source == "activity"
    assert records[0].trusted is False


def test_id_is_stable_and_per_day() -> None:
    a = aggregate([piece("2026-11-18T09:00:00-06:00", "2026-11-18T09:10:00-06:00", "Code", "x")])[0].id
    b = aggregate([piece("2026-11-18T14:00:00-06:00", "2026-11-18T14:05:00-06:00", "Code", "x")])[0].id
    c = aggregate([piece("2026-11-19T09:00:00-06:00", "2026-11-19T09:10:00-06:00", "Code", "x")])[0].id
    assert a == b
    assert a != c
    assert a.startswith("2026-11-18/")
    assert len(a) == len("2026-11-18/") + 16


def test_deny_matches_folded_text() -> None:
    deny = ["1PASSWORD", "PRIVATE BROWSING"]
    assert denied("1PASSWORD VAULT", deny)
    assert denied("FIREFOX PRIVATE BROWSING", deny)
    assert not denied("CODE GLIDER.CS", deny)


def test_cursor_round_trip() -> None:
    cursor = read_cursor("aw-watcher-window_a=2026-11-18T15:00:00+00:00;aw-watcher-window_b=2026-11-18T16:00:00Z")
    assert len(cursor) == 2
    assert cursor_at(cursor, "aw-watcher-window_b") == "2026-11-18T16:00:00Z"
    assert cursor_at(cursor, "aw-watcher-window_c") == ""
    assert len(read_cursor("")) == 0


def record(ident: str, text: str) -> FeedRecord:
    title = text.split("\n")[0]
    return feed_record("activity", ident, "2026-11-18T09:00:00-06:00", "window", title, "", text, [])


def count_in(store: IndexStore, table: str) -> int:
    return int(store.conn.execute("SELECT COUNT(*) FROM " + table).fetchone()[0])


def test_store_contract(tmp_path: Path) -> None:
    store = IndexStore(str(tmp_path / "index.sqlite"))
    records = [record("2026-11-18/aaaa", "Code: x\n2026-11-18: 10m"), record("2026-11-18/bbbb", "Docs\n2026-11-18: 5m")]

    first = FeedRead(list(records), [], False, "cursor-1", 2, "")
    r1 = apply_read(store, "activity", first, "2026-11-18T10:00:00-06:00", 0.0)
    assert r1.new == 2
    assert r1.unchanged == 0

    # The same records again: nothing is written, and the cursor is the same.
    again = FeedRead(list(records), [], False, "cursor-1", 2, "")
    r2 = apply_read(store, "activity", again, "2026-11-18T10:05:00-06:00", 0.0)
    assert r2.new == 0
    assert r2.versions == 0
    assert r2.unchanged == 2

    # One total changed: one version, the other is unchanged.
    changed = [record("2026-11-18/aaaa", "Code: x\n2026-11-18: 15m")]
    third = FeedRead(changed, [], False, "cursor-2", 1, "")
    r3 = apply_read(store, "activity", third, "2026-11-18T10:10:00-06:00", 0.0)
    assert r3.versions == 1
    assert r3.unchanged == 0

    # A read that does not cover the source (complete is False) retracts nothing, even when the
    # record it left out is stored.
    empty = FeedRead([], [], False, "cursor-2", 0, "")
    r4 = apply_read(store, "activity", empty, "2026-11-18T10:15:00-06:00", 0.0)
    assert r4.retracted == 0

    # The records are in the activity table, and not in the feeds' table.
    assert count_in(store, "fts_activity") == 3
    feeds = store.conn.execute(
        "SELECT COUNT(*) FROM fts_feeds WHERE rowid IN (SELECT rowid FROM facts WHERE source = 'activity')"
    ).fetchone()
    assert int(feeds[0]) == 0
