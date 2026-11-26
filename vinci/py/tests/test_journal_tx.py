"""The journal's transactions (journal.py JournalTx): the lock is taken and released, queued lines
are written in order by one flush, abort drops them, and a read inside a transaction sees what it
has queued. The lock itself is tested across processes by py/vinci_diff/append_race.py."""

from pathlib import Path

import pytest

from vinci_core.journal import Event, Journal, JournalTx, next_id_of
from vinci_core.timeutil import parse_instant
from vinci_core.track import track_sync

AT = "2026-11-20T12:00:00-06:00"


def line_count(path: Path) -> int:
    if not path.exists():
        return 0
    return sum(1 for raw in path.read_text(encoding="utf-8").split("\n") if raw != "")


def test_flush_writes_in_order(tmp_path: Path) -> None:
    path = str(tmp_path / "journal.jsonl")
    at = parse_instant(AT)
    tx = JournalTx(path)
    tx.begin()
    tx.append(Event(at, "idea", id="i1", text="first"))
    tx.append(Event(at, "idea", id="i2", text="second"))
    # Queued lines are seen by a read in the same transaction, and are not in the file yet.
    assert len(tx.read()) == 2
    assert line_count(Path(path)) == 0
    tx.flush()
    assert line_count(Path(path)) == 2
    tx.append(Event(at, "idea", id="i3", text="third"))
    tx.commit()
    events = Journal(path).read()
    assert len(events) == 3
    assert events[0].id == "i1"
    assert events[2].id == "i3"


def test_abort_drops_queued_lines(tmp_path: Path) -> None:
    path = str(tmp_path / "journal.jsonl")
    at = parse_instant(AT)
    tx = JournalTx(path)
    tx.begin()
    tx.append(Event(at, "idea", id="i1", text="dropped"))
    tx.abort()
    assert line_count(Path(path)) == 0
    # After abort the lock is free again: a fresh transaction can begin and commit.
    again = JournalTx(path)
    again.begin()
    again.append(Event(at, "idea", id="i1", text="kept"))
    again.commit()
    assert line_count(Path(path)) == 1


def test_nested_read_and_mint(tmp_path: Path) -> None:
    """A read inside a transaction does not wait for itself, and an id minted from that read
    continues the journal's own count."""
    path = str(tmp_path / "journal.jsonl")
    at = parse_instant(AT)
    seed = JournalTx(path)
    seed.begin()
    seed.append(Event(at, "remember", id="k1", text="one"))
    seed.commit()
    tx = JournalTx(path)
    tx.begin()
    assert len(Journal(path).read()) == 1
    assert next_id_of(tx.read(), "k") == "k2"
    assert next_id_of(tx.read(), "e") == "e1"
    tx.abort()


def test_append_needs_a_transaction(tmp_path: Path) -> None:
    path = tmp_path / "journal.jsonl"
    tx = JournalTx(str(path))
    with pytest.raises(ValueError):
        tx.append(Event(parse_instant(AT), "idea", id="i1", text="outside"))
    assert not path.exists()


def test_one_shot_append_locks(tmp_path: Path) -> None:
    """Journal.append is a transaction of one line, so it takes the lock and leaves nothing held."""
    path = str(tmp_path / "journal.jsonl")
    at = parse_instant(AT)
    Journal(path).append(Event(at, "idea", id="i1", text="one"))
    Journal(path).append(Event(at, "idea", id="i2", text="two"))
    tx = JournalTx(path)
    tx.begin()
    tx.abort()
    assert line_count(Path(path)) == 2


def test_reads_create_nothing(tmp_path: Path) -> None:
    """A read of a missing journal creates neither its folder nor the lock file."""
    folder = tmp_path / "absent"
    path = str(folder / "journal.jsonl")
    assert len(Journal(path).read()) == 0
    assert not folder.exists()
    assert len(JournalTx(path).read()) == 0
    assert not folder.exists()


def test_an_incomplete_last_line_is_ignored(tmp_path: Path) -> None:
    """A writer appends whole lines, so a reader can see a last line with no newline yet. It is
    left out; the next read sees it once it is complete."""
    path = tmp_path / "journal.jsonl"
    good = '{"at":"2026-11-20T12:00:00-06:00","kind":"idea","id":"i1","text":"one"}'
    path.write_text(good + "\n" + '{"at":"2026-11-20T12:00:00-06:00","kind":"idea","id":"i2"', encoding="utf-8")
    assert len(Journal(str(path)).read()) == 1
    path.write_text(good + "\n" + good.replace("i1", "i2") + "\n", encoding="utf-8")
    assert len(Journal(str(path)).read()) == 2


def test_a_complete_corrupt_line_is_an_error(tmp_path: Path) -> None:
    path = tmp_path / "journal.jsonl"
    good = '{"at":"2026-11-20T12:00:00-06:00","kind":"idea","id":"i1","text":"one"}'
    path.write_text(good + "\nnot json\n" + good + "\n", encoding="utf-8")
    with pytest.raises(ValueError):
        Journal(str(path)).read()


def test_a_failed_external_read_takes_no_lock(tmp_path: Path) -> None:
    """The tracker reads ActivityWatch first, with no lock. With a watch set and no answer, it has
    nothing to write, so it never takes the lock and creates no lock file."""
    path = tmp_path / "journal.jsonl"
    path.write_text(
        '{"at":"2026-11-20T12:00:00-06:00","kind":"idea","id":"i1","text":"one"}\n'
        + '{"at":"2026-11-20T12:00:00-06:00","kind":"watch","id":"i1","text":"Code"}\n',
        encoding="utf-8",
    )
    lines = track_sync(str(path), "http://127.0.0.1:1", parse_instant("2026-11-20T12:05:00-06:00"))
    assert len(lines) > 0
    assert not Path(str(path) + ".lock").exists(), "no lock file: nothing was written"
    assert line_count(path) == 2
