"""Index build tests: journal lines become facts, incrementally. Each test writes its own journal in
a temporary directory, so the real journal and staging are never read. The rebuild must equal the
incremental build, row for row, in journal order."""

import os
from pathlib import Path

import pytest

from vinci_core.index_build import chunk_body, load_records, update_from_journal
from vinci_core.index_store import IndexStore


def write_file(path: str, text: str, mode: str) -> None:
    with open(path, mode, encoding="utf-8") as f:
        f.write(text)


def dump(store: IndexStore) -> list:  # type: ignore[type-arg]
    """Every fact row in rowid order. Two stores are equal when these match."""
    return store.conn.execute(
        "SELECT source, record_id, version_hash, kind, text, cite, at,"
        " valid_from, IFNULL(valid_until, '-'), IFNULL(idea, '-'), tags, refs,"
        " trusted, status, line FROM facts ORDER BY rowid"
    ).fetchall()


def count_where(store: IndexStore, where: str) -> int:
    return int(store.conn.execute("SELECT COUNT(*) FROM facts WHERE " + where).fetchone()[0])


def test_incremental_build_reads_only_new_lines(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    l1 = '{"at":"2026-11-10T09:00:00-06:00","kind":"idea","id":"i1","text":"alpha vinci"}\n'
    l2 = '{"at":"2026-11-10T09:05:00-06:00","kind":"work","id":"i1","text":"wrote alpha tests","where":"abc123"}\n'
    l3 = '{"at":"2026-11-10T09:10:00-06:00","kind":"spent","id":"i1","value":5}\n'
    write_file(journal, l1 + l2 + l3, "w")
    store = IndexStore(dir_ + "/index.sqlite")
    first = update_from_journal(store, journal, dir_)
    assert first.lines == 3
    assert first.written == 2  # spent carries no text, so it is not a fact
    assert store.fact_total() == 2

    l4 = '{"at":"2026-11-11T09:00:00-06:00","kind":"remember","id":"k1","text":"beta fact one","tags":["fact"]}\n'
    l5 = '{"at":"2026-11-12T09:00:00-06:00","kind":"memory","id":"m2","text":"gamma correction","refs":["k1"],"tags":["from:i1"]}\n'
    # The last line is still being written: no newline yet, so it is not read.
    partial = '{"at":"2026-11-12T10:00:00-06:00","kind":"idea","id":"i2","text":"delta'
    write_file(journal, l4 + l5 + partial, "a")
    second = update_from_journal(store, journal, dir_)
    assert second.lines == 2
    assert second.written == 2
    assert second.ended == 1  # m2 refs k1, so k1 is no longer current
    assert store.fact_total() == 4

    write_file(journal, '"}\n', "a")
    third = update_from_journal(store, journal, dir_)
    assert third.lines == 1
    assert third.written == 1

    none = update_from_journal(store, journal, dir_)
    assert none.lines == 0
    assert none.written == 0
    assert store.fact_total() == 5


def test_rebuild_equals_incremental(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    first_half = (
        '{"at":"2026-11-10T09:00:00-06:00","kind":"idea","id":"i1","text":"alpha vinci"}\n'
        '{"at":"2026-11-10T09:05:00-06:00","kind":"work","id":"i1","text":"alpha work","where":"c1"}\n'
        '{"at":"2026-11-10T09:06:00-06:00","kind":"work","id":"i1","text":"alpha work again","where":"c2"}\n'
        '{"at":"2026-11-10T09:07:00-06:00","kind":"for","id":"i1","text":"first purpose"}\n'
    )
    second_half = (
        '{"at":"2026-11-11T09:00:00-06:00","kind":"remember","id":"k1","text":"beta fact","tags":["fact"]}\n'
        '{"at":"2026-11-12T09:00:00-06:00","kind":"memory","id":"m2","text":"gamma","refs":["k1"],"tags":["from:i1"]}\n'
        '{"at":"2026-11-13T09:00:00-06:00","kind":"for","id":"i1","text":"second purpose"}\n'
        '{"at":"2026-11-14T09:00:00-06:00","kind":"exchange","id":"e1","who":"assistant","text":"a reply"}\n'
    )
    write_file(journal, first_half, "w")
    incremental = IndexStore(dir_ + "/incremental.sqlite")
    update_from_journal(incremental, journal, dir_)
    write_file(journal, second_half, "a")
    update_from_journal(incremental, journal, dir_)

    rebuilt = IndexStore(dir_ + "/rebuilt.sqlite")
    update_from_journal(rebuilt, journal, dir_)

    assert dump(incremental) == dump(rebuilt)
    assert incremental.meta_get("journal_offset") == rebuilt.meta_get("journal_offset")


def test_records_map_to_facts_by_kind(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    write_file(
        journal,
        '{"at":"2026-11-10T09:00:00-06:00","kind":"idea","id":"i1","text":"alpha vinci"}\n'
        '{"at":"2026-11-10T09:05:00-06:00","kind":"work","id":"i1","text":"alpha work"}\n'
        '{"at":"2026-11-10T09:06:00-06:00","kind":"req","id":"r1","refs":["i1"],"text":"needs a bank"}\n'
        '{"at":"2026-11-10T09:07:00-06:00","kind":"sync"}\n'
        '{"at":"2026-11-10T09:08:00-06:00","kind":"stage","id":"i1","text":"incubate"}\n'
        '{"at":"2026-11-10T09:09:00-06:00","kind":"bookmark","id":"b1","text":"Bun","url":"https://bun.sh","why":"runtime","tags":["js"]}\n'
        '{"at":"2026-11-10T09:10:00-06:00","kind":"memory","id":"m1","text":"fact from i1","tags":["from:i1"]}\n'
        '{"at":"2026-11-10T09:11:00-06:00","kind":"memory","id":"m2","text":"archived one","tags":["archived"]}\n'
        '{"at":"2026-11-10T09:12:00-06:00","kind":"exchange","id":"e1","who":"user","text":"my question"}\n'
        '{"at":"2026-11-10T09:13:00-06:00","kind":"exchange","id":"e2","who":"assistant","text":"an answer"}\n'
        '{"at":"2026-11-10T09:14:00-06:00","kind":"mystery","id":"x9","text":"unknown kinds are skipped"}\n',
        "w",
    )
    store = IndexStore(dir_ + "/index.sqlite")
    report = update_from_journal(store, journal, dir_)
    assert report.lines == 11
    # stage, sync and the unknown kind carry no fact.
    assert store.fact_total() == 8
    assert count_where(store, "kind = 'stage'") == 0
    assert count_where(store, "kind = 'mystery'") == 0
    # The idea, its work and req, and the memory from i1 all name idea i1.
    assert count_where(store, "idea = 'i1'") == 4
    assert count_where(store, "cite = 'm1' AND idea = 'i1'") == 1
    # The bookmark's content carries its why and url, so a search for either finds it.
    bookmark = store.conn.execute("SELECT text FROM facts WHERE kind = 'bookmark'").fetchone()
    assert bookmark[0] == "Bun\nruntime\nhttps://bun.sh"
    # Archived memories are stored, but are not active.
    assert count_where(store, "status = 'archived'") == 1
    # Trust: the user's exchange is trusted, the assistant's is not.
    assert count_where(store, "cite = 'e1' AND trusted = 1") == 1
    assert count_where(store, "cite = 'e2' AND trusted = 0") == 1


def test_work_content_carries_its_commit(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    write_file(
        journal,
        '{"at":"2026-10-27T18:12:21-05:00","kind":"work","id":"i4","text":"Add evaluate operation",'
        '"where":"ec91dd83151afe86fa875c8d7ea0fb1b1b79013d"}\n',
        "w",
    )
    store = IndexStore(dir_ + "/index.sqlite")
    update_from_journal(store, journal, dir_)
    row = store.conn.execute("SELECT text FROM facts").fetchone()
    assert row[0] == "Add evaluate operation\ncommit ec91dd83151afe86fa875c8d7ea0fb1b1b79013d"


def test_same_id_repeated_by_an_appended_kind_is_a_new_fact(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    write_file(
        journal,
        '{"at":"2026-11-10T09:05:00-06:00","kind":"work","id":"i1","text":"first work"}\n'
        '{"at":"2026-11-10T09:06:00-06:00","kind":"work","id":"i1","text":"second work"}\n',
        "w",
    )
    store = IndexStore(dir_ + "/index.sqlite")
    update_from_journal(store, journal, dir_)
    # Work lines are events, not corrections: both stay current.
    assert count_where(store, "kind = 'work' AND valid_until IS NULL") == 2


def test_a_repeated_for_line_replaces_the_earlier_one(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    write_file(
        journal,
        '{"at":"2026-11-10T09:07:00-06:00","kind":"for","id":"i1","text":"first purpose"}\n'
        '{"at":"2026-11-13T09:00:00-06:00","kind":"for","id":"i1","text":"second purpose"}\n',
        "w",
    )
    store = IndexStore(dir_ + "/index.sqlite")
    update_from_journal(store, journal, dir_)
    assert count_where(store, "kind = 'for' AND valid_until IS NULL") == 1
    assert count_where(store, "text = 'first purpose' AND valid_until = '2026-11-13T09:00:00-06:00'") == 1


def test_status_tags_are_kept_and_not_active(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    write_file(
        journal,
        '{"at":"2026-11-10T09:00:00-06:00","kind":"memory","id":"m1","text":"kept","tags":["fact"]}\n'
        '{"at":"2026-11-11T09:00:00-06:00","kind":"remember","id":"k2","text":"gone","tags":["retracted"]}\n',
        "w",
    )
    store = IndexStore(dir_ + "/index.sqlite")
    update_from_journal(store, journal, dir_)
    assert count_where(store, "status = 'active'") == 1
    assert count_where(store, "status = 'retracted' AND valid_until IS NULL") == 1


def test_read_line_gets_untrusted_chunks(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    os.mkdir(dir_ + "/docs")
    write_file(dir_ + "/docs/abc.md", "# Intro\nFirst text here.\n# Second\nMore text.", "w")
    write_file(
        journal,
        '{"at":"2026-11-15T00:00:00+00:00","kind":"read","id":"d1","text":"Read check",'
        '"url":"scratch/read-check.md","where":"docs/abc.md"}\n',
        "w",
    )
    store = IndexStore(dir_ + "/index.sqlite")
    report = update_from_journal(store, journal, dir_)
    assert report.bodies == 1
    assert store.fact_total() == 3  # the title line and two chunks
    assert count_where(store, "kind = 'read' AND trusted = 0") == 2
    assert count_where(store, "cite = 'd1' AND trusted = 1") == 1
    assert count_where(store, "cite = 'd1/2' AND text = '# Second' || char(10) || 'More text.'") == 1


def test_missing_body_is_counted_not_fatal(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    write_file(
        journal,
        '{"at":"2026-11-15T00:00:00+00:00","kind":"read","id":"d2","text":"No body","where":"docs/none.md"}\n',
        "w",
    )
    store = IndexStore(dir_ + "/index.sqlite")
    report = update_from_journal(store, journal, dir_)
    assert report.missing_bodies == 1
    assert store.fact_total() == 1


def test_a_bad_line_rolls_the_whole_run_back(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    write_file(journal, '{"at":"2026-11-10T09:00:00-06:00","kind":"idea","id":"i1","text":"good"}\n', "w")
    store = IndexStore(dir_ + "/index.sqlite")
    update_from_journal(store, journal, dir_)
    offset_before = store.meta_get("journal_offset")
    write_file(journal, "this is not json\n", "a")
    with pytest.raises(ValueError):
        update_from_journal(store, journal, dir_)
    assert store.fact_total() == 1
    assert store.meta_get("journal_offset") == offset_before


def test_a_shorter_journal_is_refused(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    write_file(journal, '{"at":"2026-11-10T09:00:00-06:00","kind":"idea","id":"i1","text":"good"}\n', "w")
    store = IndexStore(dir_ + "/index.sqlite")
    update_from_journal(store, journal, dir_)
    write_file(journal, "", "w")
    with pytest.raises(ValueError):
        update_from_journal(store, journal, dir_)


def test_chunk_body_splits_at_headings_and_size() -> None:
    assert len(chunk_body("")) == 0
    assert len(chunk_body("one line\nand another")) == 1
    # A heading always starts a chunk, even a short one.
    sections = chunk_body("# A\ntext a\n# B\ntext b")
    assert sections == ["# A\ntext a", "# B\ntext b"]
    # Without headings, lines are packed to about 1000 bytes and never split.
    line = "x" * 400
    chunks = chunk_body(line + "\n" + line + "\n" + line + "\n" + line)
    assert len(chunks) == 2
    for c in chunks:
        assert len(c.encode("utf-8")) <= 1000, "a chunk over 1000 bytes"


def test_load_records_versions_by_hash(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    path = dir_ + "/records.jsonl"
    write_file(path, '{"source":"lme","id":"s1-t1","at":"2026-01-01T00:00:00Z","kind":"turn","text":"A cat sat","hash":"hA","trusted":false,"links":[]}\n', "w")
    store = IndexStore(dir_ + "/records.sqlite")
    assert load_records(store, path).written == 1
    write_file(path, '{"source":"lme","id":"s1-t1","at":"2026-01-01T00:00:00Z","kind":"turn","text":"A dog ran","hash":"hB","trusted":false,"links":[]}\n', "w")
    assert load_records(store, path).written == 1
    write_file(path, '{"source":"lme","id":"s1-t1","at":"2026-01-01T00:00:00Z","kind":"turn","text":"A cat sat","hash":"hA","trusted":false,"links":[]}\n', "w")
    back = load_records(store, path)
    assert back.written == 1
    assert back.unchanged == 0
    assert back.ended == 1  # the hB version ended when hA came back
    assert store.fact_total() == 3
    assert count_where(store, "valid_until IS NULL") == 1
    assert count_where(store, "version_hash = 'hA' AND valid_until IS NULL") == 1
