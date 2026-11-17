# Index build tests: journal lines become facts, incrementally. Each test writes its
# own journal in a temporary directory, so the real journal and staging are never read.
# The rebuild must equal the incremental build, row for row, in journal order.

from std.python import Python, PythonObject
from std.testing import assert_equal, assert_false, assert_true

from index_build import (
    BuildReport,
    chunk_body,
    load_records,
    update_from_journal,
)
from index_store import IndexStore


def temp_dir() raises -> String:
    var tempfile = Python.import_module("tempfile")
    return String(tempfile.mkdtemp())


def write_file(path: String, text: String, mode: String) raises:
    var builtins = Python.import_module("builtins")
    var f = builtins.open(PythonObject(path), PythonObject(mode))
    _ = f.write(PythonObject(text))
    f.close()


def dump(store: IndexStore) raises -> String:
    """Every fact row in rowid order, as text. Two stores are equal when these match.
    """
    var rows = store.conn.execute(
        "SELECT source, record_id, version_hash, kind, text, cite, at,"
        " valid_from, IFNULL(valid_until, '-'), IFNULL(idea, '-'), tags, refs,"
        " trusted, status, line FROM facts ORDER BY rowid"
    ).fetchall()
    return String(rows)


def count_where(store: IndexStore, where_: String) raises -> Int:
    var row = store.conn.execute(
        "SELECT COUNT(*) FROM facts WHERE " + where_
    ).fetchone()
    return Int(String(row[0]))


def test_incremental_build_reads_only_new_lines() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    var l1 = (
        '{"at":"2026-11-10T09:00:00-06:00","kind":"idea","id":"i1","text":"alpha'
        ' vinci"}\n'
    )
    var l2 = (
        '{"at":"2026-11-10T09:05:00-06:00","kind":"work","id":"i1","text":"wrote'
        ' alpha tests","where":"abc123"}\n'
    )
    var l3 = '{"at":"2026-11-10T09:10:00-06:00","kind":"spent","id":"i1","value":5}\n'
    write_file(journal, l1 + l2 + l3, "w")
    var store = IndexStore(dir + "/index.sqlite")
    var first = update_from_journal(store, journal, dir)
    assert_equal(first.lines, 3)
    assert_equal(first.written, 2)  # spent carries no text, so it is not a fact
    assert_equal(store.fact_total(), 2)

    var l4 = (
        '{"at":"2026-11-11T09:00:00-06:00","kind":"remember","id":"k1","text":"beta'
        ' fact one","tags":["fact"]}\n'
    )
    var l5 = (
        '{"at":"2026-11-12T09:00:00-06:00","kind":"memory","id":"m2","text":"gamma'
        ' correction","refs":["k1"],"tags":["from:i1"]}\n'
    )
    # The last line is still being written: no newline yet, so it is not read.
    var partial = '{"at":"2026-11-12T10:00:00-06:00","kind":"idea","id":"i2","text":"delta'
    write_file(journal, l4 + l5 + partial, "a")
    var second = update_from_journal(store, journal, dir)
    assert_equal(second.lines, 2)
    assert_equal(second.written, 2)
    assert_equal(second.ended, 1)  # m2 refs k1, so k1 is no longer current
    assert_equal(store.fact_total(), 4)

    write_file(journal, '"}\n', "a")
    var third = update_from_journal(store, journal, dir)
    assert_equal(third.lines, 1)
    assert_equal(third.written, 1)

    var none = update_from_journal(store, journal, dir)
    assert_equal(none.lines, 0)
    assert_equal(none.written, 0)
    assert_equal(store.fact_total(), 5)


def test_rebuild_equals_incremental() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    var first_half = (
        '{"at":"2026-11-10T09:00:00-06:00","kind":"idea","id":"i1","text":"alpha'
        ' vinci"}\n'
        + '{"at":"2026-11-10T09:05:00-06:00","kind":"work","id":"i1","text":"alpha'
        ' work","where":"c1"}\n'
        + '{"at":"2026-11-10T09:06:00-06:00","kind":"work","id":"i1","text":"alpha'
        ' work again","where":"c2"}\n'
        + '{"at":"2026-11-10T09:07:00-06:00","kind":"for","id":"i1","text":"first'
        ' purpose"}\n'
    )
    var second_half = (
        '{"at":"2026-11-11T09:00:00-06:00","kind":"remember","id":"k1","text":"beta'
        ' fact","tags":["fact"]}\n'
        + '{"at":"2026-11-12T09:00:00-06:00","kind":"memory","id":"m2","text":"gamma","refs":["k1"],"tags":["from:i1"]}\n'
        + '{"at":"2026-11-13T09:00:00-06:00","kind":"for","id":"i1","text":"second'
        ' purpose"}\n'
        + '{"at":"2026-11-14T09:00:00-06:00","kind":"exchange","id":"e1","who":"assistant","text":"a'
        ' reply"}\n'
    )
    write_file(journal, first_half, "w")
    var incremental = IndexStore(dir + "/incremental.sqlite")
    _ = update_from_journal(incremental, journal, dir)
    write_file(journal, second_half, "a")
    _ = update_from_journal(incremental, journal, dir)

    var rebuilt = IndexStore(dir + "/rebuilt.sqlite")
    _ = update_from_journal(rebuilt, journal, dir)

    assert_equal(dump(incremental), dump(rebuilt))
    assert_equal(
        incremental.meta_get("journal_offset"),
        rebuilt.meta_get("journal_offset"),
    )


def test_records_map_to_facts_by_kind() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    write_file(
        journal,
        '{"at":"2026-11-10T09:00:00-06:00","kind":"idea","id":"i1","text":"alpha'
        ' vinci"}\n'
        + '{"at":"2026-11-10T09:05:00-06:00","kind":"work","id":"i1","text":"alpha'
        ' work"}\n'
        + '{"at":"2026-11-10T09:06:00-06:00","kind":"req","id":"r1","refs":["i1"],"text":"needs'
        ' a bank"}\n'
        + '{"at":"2026-11-10T09:07:00-06:00","kind":"sync"}\n'
        + '{"at":"2026-11-10T09:08:00-06:00","kind":"stage","id":"i1","text":"incubate"}\n'
        + '{"at":"2026-11-10T09:09:00-06:00","kind":"bookmark","id":"b1","text":"Bun","url":"https://bun.sh","why":"runtime","tags":["js"]}\n'
        + '{"at":"2026-11-10T09:10:00-06:00","kind":"memory","id":"m1","text":"fact'
        ' from i1","tags":["from:i1"]}\n'
        + '{"at":"2026-11-10T09:11:00-06:00","kind":"memory","id":"m2","text":"archived'
        ' one","tags":["archived"]}\n'
        + '{"at":"2026-11-10T09:12:00-06:00","kind":"exchange","id":"e1","who":"user","text":"my'
        ' question"}\n'
        + '{"at":"2026-11-10T09:13:00-06:00","kind":"exchange","id":"e2","who":"assistant","text":"an'
        ' answer"}\n'
        + '{"at":"2026-11-10T09:14:00-06:00","kind":"mystery","id":"x9","text":"unknown'
        ' kinds are skipped"}\n',
        "w",
    )
    var store = IndexStore(dir + "/index.sqlite")
    var report = update_from_journal(store, journal, dir)
    assert_equal(report.lines, 11)
    # stage, sync and the unknown kind carry no fact.
    assert_equal(store.fact_total(), 8)
    assert_equal(count_where(store, "kind = 'stage'"), 0)
    assert_equal(count_where(store, "kind = 'mystery'"), 0)
    # The idea, its work and req, and the memory from i1 all name idea i1.
    assert_equal(count_where(store, "idea = 'i1'"), 4)
    assert_equal(count_where(store, "cite = 'm1' AND idea = 'i1'"), 1)
    # The bookmark's content carries its why and url, so a search for either finds it.
    var bookmark = store.conn.execute(
        "SELECT text FROM facts WHERE kind = 'bookmark'"
    ).fetchone()
    assert_equal(String(bookmark[0]), "Bun\nruntime\nhttps://bun.sh")
    # Archived memories are stored, but are not active.
    assert_equal(count_where(store, "status = 'archived'"), 1)
    # Trust: the user's exchange is trusted, the assistant's is not.
    assert_equal(count_where(store, "cite = 'e1' AND trusted = 1"), 1)
    assert_equal(count_where(store, "cite = 'e2' AND trusted = 0"), 1)


def test_work_content_carries_its_commit() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    write_file(
        journal,
        (
            '{"at":"2026-10-27T18:12:21-05:00","kind":"work","id":"i4","text":"Add'
            " evaluate"
            ' operation","where":"ec91dd83151afe86fa875c8d7ea0fb1b1b79013d"}\n'
        ),
        "w",
    )
    var store = IndexStore(dir + "/index.sqlite")
    _ = update_from_journal(store, journal, dir)
    var row = store.conn.execute("SELECT text FROM facts").fetchone()
    assert_equal(
        String(row[0]),
        (
            "Add evaluate operation\ncommit"
            " ec91dd83151afe86fa875c8d7ea0fb1b1b79013d"
        ),
    )


def test_same_id_repeated_by_an_appended_kind_is_a_new_fact() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    write_file(
        journal,
        '{"at":"2026-11-10T09:05:00-06:00","kind":"work","id":"i1","text":"first'
        ' work"}\n'
        + '{"at":"2026-11-10T09:06:00-06:00","kind":"work","id":"i1","text":"second'
        ' work"}\n',
        "w",
    )
    var store = IndexStore(dir + "/index.sqlite")
    _ = update_from_journal(store, journal, dir)
    # Work lines are events, not corrections: both stay current.
    assert_equal(count_where(store, "kind = 'work' AND valid_until IS NULL"), 2)


def test_a_repeated_for_line_replaces_the_earlier_one() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    write_file(
        journal,
        '{"at":"2026-11-10T09:07:00-06:00","kind":"for","id":"i1","text":"first'
        ' purpose"}\n'
        + '{"at":"2026-11-13T09:00:00-06:00","kind":"for","id":"i1","text":"second'
        ' purpose"}\n',
        "w",
    )
    var store = IndexStore(dir + "/index.sqlite")
    _ = update_from_journal(store, journal, dir)
    assert_equal(count_where(store, "kind = 'for' AND valid_until IS NULL"), 1)
    assert_equal(
        count_where(
            store,
            (
                "text = 'first purpose' AND valid_until ="
                " '2026-11-13T09:00:00-06:00'"
            ),
        ),
        1,
    )


def test_status_tags_are_kept_and_not_active() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    write_file(
        journal,
        '{"at":"2026-11-10T09:00:00-06:00","kind":"memory","id":"m1","text":"kept","tags":["fact"]}\n'
        + '{"at":"2026-11-11T09:00:00-06:00","kind":"remember","id":"k2","text":"gone","tags":["retracted"]}\n',
        "w",
    )
    var store = IndexStore(dir + "/index.sqlite")
    _ = update_from_journal(store, journal, dir)
    assert_equal(count_where(store, "status = 'active'"), 1)
    assert_equal(
        count_where(store, "status = 'retracted' AND valid_until IS NULL"), 1
    )


def test_read_line_gets_untrusted_chunks() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    var os = Python.import_module("os")
    _ = os.mkdir(PythonObject(dir + "/docs"))
    write_file(
        dir + "/docs/abc.md",
        "# Intro\nFirst text here.\n# Second\nMore text.",
        "w",
    )
    write_file(
        journal,
        (
            '{"at":"2026-11-15T00:00:00+00:00","kind":"read","id":"d1","text":"Read'
            ' check","url":"scratch/read-check.md","where":"docs/abc.md"}\n'
        ),
        "w",
    )
    var store = IndexStore(dir + "/index.sqlite")
    var report = update_from_journal(store, journal, dir)
    assert_equal(report.bodies, 1)
    assert_equal(store.fact_total(), 3)  # the title line and two chunks
    assert_equal(count_where(store, "kind = 'read' AND trusted = 0"), 2)
    assert_equal(count_where(store, "cite = 'd1' AND trusted = 1"), 1)
    assert_equal(
        count_where(
            store,
            "cite = 'd1/2' AND text = '# Second' || char(10) || 'More text.'",
        ),
        1,
    )


def test_missing_body_is_counted_not_fatal() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    write_file(
        journal,
        (
            '{"at":"2026-11-15T00:00:00+00:00","kind":"read","id":"d2","text":"No'
            ' body","where":"docs/none.md"}\n'
        ),
        "w",
    )
    var store = IndexStore(dir + "/index.sqlite")
    var report = update_from_journal(store, journal, dir)
    assert_equal(report.missing_bodies, 1)
    assert_equal(store.fact_total(), 1)


def test_a_bad_line_rolls_the_whole_run_back() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    write_file(
        journal,
        '{"at":"2026-11-10T09:00:00-06:00","kind":"idea","id":"i1","text":"good"}\n',
        "w",
    )
    var store = IndexStore(dir + "/index.sqlite")
    _ = update_from_journal(store, journal, dir)
    var offset_before = store.meta_get("journal_offset")
    write_file(journal, "this is not json\n", "a")
    var refused = False
    try:
        _ = update_from_journal(store, journal, dir)
    except:
        refused = True
    assert_true(refused, "a bad line must stop the run")
    assert_equal(store.fact_total(), 1)
    assert_equal(store.meta_get("journal_offset"), offset_before)


def test_a_shorter_journal_is_refused() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    write_file(
        journal,
        '{"at":"2026-11-10T09:00:00-06:00","kind":"idea","id":"i1","text":"good"}\n',
        "w",
    )
    var store = IndexStore(dir + "/index.sqlite")
    _ = update_from_journal(store, journal, dir)
    write_file(journal, "", "w")
    var refused = False
    try:
        _ = update_from_journal(store, journal, dir)
    except:
        refused = True
    assert_true(refused, "a journal shorter than the offset must be refused")


def test_chunk_body_splits_at_headings_and_size() raises:
    assert_equal(len(chunk_body("")), 0)
    assert_equal(len(chunk_body("one line\nand another")), 1)
    # A heading always starts a chunk, even a short one.
    var sections = chunk_body("# A\ntext a\n# B\ntext b")
    assert_equal(len(sections), 2)
    assert_equal(sections[0], "# A\ntext a")
    assert_equal(sections[1], "# B\ntext b")
    # Without headings, lines are packed to about 1000 bytes and never split.
    var line = String()
    for _ in range(400):
        line += "x"
    var big = line + "\n" + line + "\n" + line + "\n" + line
    var chunks = chunk_body(big)
    assert_equal(len(chunks), 2)
    for c in chunks:
        assert_true(c.byte_length() <= 1000, "a chunk over 1000 bytes")


def test_load_records_versions_by_hash() raises:
    var dir = temp_dir()
    var path = dir + "/records.jsonl"
    write_file(
        path,
        (
            '{"source":"lme","id":"s1-t1","at":"2026-01-01T00:00:00Z","kind":"turn","text":"A'
            ' cat sat","hash":"hA","trusted":false,"links":[]}\n'
        ),
        "w",
    )
    var store = IndexStore(dir + "/records.sqlite")
    assert_equal(load_records(store, path).written, 1)
    write_file(
        path,
        (
            '{"source":"lme","id":"s1-t1","at":"2026-01-01T00:00:00Z","kind":"turn","text":"A'
            ' dog ran","hash":"hB","trusted":false,"links":[]}\n'
        ),
        "w",
    )
    assert_equal(load_records(store, path).written, 1)
    write_file(
        path,
        (
            '{"source":"lme","id":"s1-t1","at":"2026-01-01T00:00:00Z","kind":"turn","text":"A'
            ' cat sat","hash":"hA","trusted":false,"links":[]}\n'
        ),
        "w",
    )
    var back = load_records(store, path)
    assert_equal(back.written, 1)
    assert_equal(back.unchanged, 0)
    assert_equal(back.ended, 1)  # the hB version ended when hA came back
    assert_equal(store.fact_total(), 3)
    assert_equal(count_where(store, "valid_until IS NULL"), 1)
    assert_equal(
        count_where(store, "version_hash = 'hA' AND valid_until IS NULL"), 1
    )


def main() raises:
    test_incremental_build_reads_only_new_lines()
    print("test_incremental_build_reads_only_new_lines: ok")
    test_rebuild_equals_incremental()
    print("test_rebuild_equals_incremental: ok")
    test_records_map_to_facts_by_kind()
    print("test_records_map_to_facts_by_kind: ok")
    test_work_content_carries_its_commit()
    print("test_work_content_carries_its_commit: ok")
    test_same_id_repeated_by_an_appended_kind_is_a_new_fact()
    print("test_same_id_repeated_by_an_appended_kind_is_a_new_fact: ok")
    test_a_repeated_for_line_replaces_the_earlier_one()
    print("test_a_repeated_for_line_replaces_the_earlier_one: ok")
    test_status_tags_are_kept_and_not_active()
    print("test_status_tags_are_kept_and_not_active: ok")
    test_read_line_gets_untrusted_chunks()
    print("test_read_line_gets_untrusted_chunks: ok")
    test_missing_body_is_counted_not_fatal()
    print("test_missing_body_is_counted_not_fatal: ok")
    test_a_bad_line_rolls_the_whole_run_back()
    print("test_a_bad_line_rolls_the_whole_run_back: ok")
    test_a_shorter_journal_is_refused()
    print("test_a_shorter_journal_is_refused: ok")
    test_chunk_body_splits_at_headings_and_size()
    print("test_chunk_body_splits_at_headings_and_size: ok")
    test_load_records_versions_by_hash()
    print("test_load_records_versions_by_hash: ok")
