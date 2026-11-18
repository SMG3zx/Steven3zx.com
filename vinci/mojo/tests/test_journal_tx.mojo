# The journal's transactions (journal.mojo JournalTx): the lock is taken and released, queued
# lines are written in order by one flush, abort drops them, and a read inside a transaction sees
# what it has queued. The lock itself is tested across processes by py/vinci_diff/append_race.py.

from std.os import makedirs, remove
from std.pathlib import Path
from std.python import Python, PythonObject
from std.testing import assert_equal, assert_false, assert_true

from journal import Event, Journal, JournalTx, next_id_of
from timeutil import parse_instant
from track import track_sync


def scratch_dir() raises -> String:
    """A folder for this run only, so two runs of the suite (make check in two shells) cannot
    share a journal."""
    var os = Python.import_module("os")
    return "build/test-journal-tx-" + String(os.getpid())


def journal_path() raises -> String:
    return scratch_dir() + "/journal.jsonl"


def fresh() raises:
    makedirs(scratch_dir(), exist_ok=True)
    if Path(journal_path()).exists():
        remove(journal_path())
    var lock = journal_path() + ".lock"
    if Path(lock).exists():
        remove(lock)


def line_count() raises -> Int:
    if not Path(journal_path()).exists():
        return 0
    var n = 0
    for raw in Path(journal_path()).read_text().split("\n"):
        if String(raw).byte_length() > 0:
            n += 1
    return n


def test_flush_writes_in_order() raises:
    fresh()
    var at = parse_instant("2026-11-20T12:00:00-06:00")
    var tx = JournalTx(journal_path())
    tx.begin()
    tx.append(Event(at.copy(), "idea", "i1", "first"))
    tx.append(Event(at.copy(), "idea", "i2", "second"))
    # Queued lines are seen by a read in the same transaction, and are not in the file yet.
    assert_equal(len(tx.read()), 2)
    assert_equal(line_count(), 0)
    tx.flush()
    assert_equal(line_count(), 2)
    tx.append(Event(at.copy(), "idea", "i3", "third"))
    tx.commit()
    var events = Journal(journal_path()).read()
    assert_equal(len(events), 3)
    assert_equal(events[0].id.value(), "i1")
    assert_equal(events[2].id.value(), "i3")


def test_abort_drops_queued_lines() raises:
    fresh()
    var at = parse_instant("2026-11-20T12:00:00-06:00")
    var tx = JournalTx(journal_path())
    tx.begin()
    tx.append(Event(at.copy(), "idea", "i1", "dropped"))
    tx.abort()
    assert_equal(line_count(), 0)
    # After abort the lock is free again: a fresh transaction can begin and commit.
    var again = JournalTx(journal_path())
    again.begin()
    again.append(Event(at.copy(), "idea", "i1", "kept"))
    again.commit()
    assert_equal(line_count(), 1)


def test_nested_read_and_mint() raises:
    """A read inside a transaction (the same file, the same process) does not wait for itself,
    and an id minted from that read continues the journal's own count."""
    fresh()
    var at = parse_instant("2026-11-20T12:00:00-06:00")
    var seed = JournalTx(journal_path())
    seed.begin()
    seed.append(Event(at.copy(), "remember", "k1", "one"))
    seed.commit()
    var tx = JournalTx(journal_path())
    tx.begin()
    var inner = Journal(journal_path()).read()
    assert_equal(len(inner), 1)
    assert_equal(next_id_of(tx.read(), "k"), "k2")
    assert_equal(next_id_of(tx.read(), "e"), "e1")
    tx.abort()


def test_append_needs_a_transaction() raises:
    fresh()
    var at = parse_instant("2026-11-20T12:00:00-06:00")
    var tx = JournalTx(journal_path())
    var raised = False
    try:
        tx.append(Event(at.copy(), "idea", "i1", "outside"))
    except:
        raised = True
    assert_true(raised, "a line queued outside a transaction must be refused")
    assert_false(Path(journal_path()).exists())


def test_one_shot_append_locks() raises:
    """Journal.append is a transaction of one line, so it takes the lock and leaves nothing
    held: a following transaction begins at once."""
    fresh()
    var at = parse_instant("2026-11-20T12:00:00-06:00")
    Journal(journal_path()).append(Event(at.copy(), "idea", "i1", "one"))
    Journal(journal_path()).append(Event(at.copy(), "idea", "i2", "two"))
    var tx = JournalTx(journal_path())
    tx.begin()
    tx.abort()
    assert_equal(line_count(), 2)


def test_reads_create_nothing() raises:
    """A read of a missing journal creates neither its folder nor the lock file, and takes no
    lock: a read works while a transaction holds it in the same process."""
    var shutil = Python.import_module("shutil")
    var dir = scratch_dir() + "/absent"
    _ = shutil.rmtree(PythonObject(dir), ignore_errors=True)
    var path = dir + "/journal.jsonl"
    assert_equal(len(Journal(path).read()), 0)
    assert_false(Path(dir).exists())
    var none = JournalTx(path)
    assert_equal(len(none.read()), 0)
    assert_false(Path(dir).exists(), "a read-only transaction creates nothing")


def test_an_incomplete_last_line_is_ignored() raises:
    """A writer appends whole lines, so a reader can see a last line with no newline yet. It is
    left out; the next read sees it once it is complete."""
    fresh()
    var good = '{"at":"2026-11-20T12:00:00-06:00","kind":"idea","id":"i1","text":"one"}'
    Path(journal_path()).write_text(
        good
        + "\n"
        + '{"at":"2026-11-20T12:00:00-06:00","kind":"idea","id":"i2"'
    )
    assert_equal(len(Journal(journal_path()).read()), 1)
    Path(journal_path()).write_text(
        good + "\n" + good.replace("i1", "i2") + "\n"
    )
    assert_equal(len(Journal(journal_path()).read()), 2)


def test_a_complete_corrupt_line_is_an_error() raises:
    fresh()
    var good = '{"at":"2026-11-20T12:00:00-06:00","kind":"idea","id":"i1","text":"one"}'
    Path(journal_path()).write_text(good + "\nnot json\n" + good + "\n")
    var raised = False
    try:
        _ = Journal(journal_path()).read()
    except:
        raised = True
    assert_true(
        raised, "a complete line that does not parse must still be an error"
    )


def test_a_failed_external_read_takes_no_lock() raises:
    """The tracker reads ActivityWatch first, with no lock. With a watch set and no answer, it
    has nothing to write, so it never takes the lock and creates no lock file.
    """
    var dir = scratch_dir() + "/tracker"
    var shutil = Python.import_module("shutil")
    _ = shutil.rmtree(PythonObject(dir), ignore_errors=True)
    makedirs(dir, exist_ok=True)
    var path = dir + "/journal.jsonl"
    # Written directly: Journal.append would take the lock and create the file under test.
    Path(path).write_text(
        '{"at":"2026-11-20T12:00:00-06:00","kind":"idea","id":"i1","text":"one"}\n'
        + '{"at":"2026-11-20T12:00:00-06:00","kind":"watch","id":"i1","text":"Code"}\n'
    )
    var lines = track_sync(
        path, "http://127.0.0.1:1", parse_instant("2026-11-20T12:05:00-06:00")
    )
    assert_true(len(lines) > 0)
    assert_false(
        Path(path + ".lock").exists(), "no lock file: nothing was written"
    )
    assert_equal(line_count_at(path), 2)


def line_count_at(path: String) raises -> Int:
    var n = 0
    for raw in Path(path).read_text().split("\n"):
        if String(raw).byte_length() > 0:
            n += 1
    return n


def main() raises:
    test_flush_writes_in_order()
    test_abort_drops_queued_lines()
    test_nested_read_and_mint()
    test_append_needs_a_transaction()
    test_one_shot_append_locks()
    test_reads_create_nothing()
    test_an_incomplete_last_line_is_ignored()
    test_a_complete_corrupt_line_is_an_error()
    test_a_failed_external_read_takes_no_lock()
    var shutil = Python.import_module("shutil")
    _ = shutil.rmtree(PythonObject(scratch_dir()), ignore_errors=True)
    print("test_journal_tx: ok")
