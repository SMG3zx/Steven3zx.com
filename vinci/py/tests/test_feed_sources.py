"""Recall by source (M5 step a2): parse_sources, the journal and feed search tables kept apart, the
default of journal only, the feed sources on request, and the source label in the packed text. A
temporary index in each test; the real index is never opened."""

import pytest

from vinci_core.embed_config import default_config
from vinci_core.feed_store import feed_record, put_record
from vinci_core.fusion import Candidate
from vinci_core.index_store import Fact, IndexStore
from vinci_core.pack import estimate_counter, pack
from vinci_core.recall import Settings, merge_reserved, parse_sources, recall
from vinci_core.session import UserError


def journal_fact(store: IndexStore, cite: str, text: str) -> None:
    store.begin()
    store.put(Fact("journal", "idea:" + cite, "hash-" + cite, "idea", text, cite, "2026-11-10T09:00:00-06:00", None, [], [], True, "active", 1))
    store.commit()


def settings_for(sources: list[str]) -> Settings:
    s = Settings(10, ["lexical"], 0, "2026-11-18", default_config())
    s.sources = list(sources)
    return s


def build_index(tmp_path) -> IndexStore:  # type: ignore[no-untyped-def]
    store = IndexStore(str(tmp_path / "index.sqlite"))
    journal_fact(store, "i1", "Ship the lantern prototype this week")
    store.begin()
    put_record(
        store,
        feed_record(
            "git",
            "abc1234567",
            "2026-11-17T10:00:00+00:00",
            "commit",
            "Ship the lantern",
            "",
            "Ship the lantern\n\ncommit abc1234567\nChanged 1 files, +1 -0\n",
            [],
        ),
    )
    put_record(
        store,
        feed_record(
            "vaults",
            "Notes/lantern.md#lantern",
            "2026-11-16T10:00:00+00:00",
            "section",
            "lantern",
            "/x/Notes/lantern.md",
            "lantern\n\n# Lantern\n\nThe lantern is a note.\n",
            [],
        ),
    )
    store.commit()
    return store


def test_parse_sources() -> None:
    assert len(parse_sources("all")) == 3
    pair = parse_sources("git,vaults")
    assert len(pair) == 2
    assert pair[0] == "git"
    assert len(parse_sources("journal")) == 1
    with pytest.raises(UserError):
        parse_sources("journal,bogus")
    # activity is named on its own: all stays git, vaults and the journal.
    activity = parse_sources("activity,git")
    assert len(activity) == 2
    assert activity[0] == "activity"


def test_feed_records_stay_out_of_the_journal_table(tmp_path) -> None:  # type: ignore[no-untyped-def]
    store = build_index(tmp_path)
    rows = store.conn.execute("SELECT COUNT(*) FROM fts JOIN facts AS f ON f.rowid = fts.rowid WHERE f.source <> 'journal'").fetchone()
    assert int(rows[0]) == 0
    feed_rows = store.conn.execute("SELECT COUNT(*) FROM fts_feeds").fetchone()
    assert int(feed_rows[0]) == 2


def test_default_is_journal_only(tmp_path) -> None:  # type: ignore[no-untyped-def]
    store = build_index(tmp_path)
    found = recall(store, "lantern", settings_for(["journal"]))
    assert len(found.results) == 1
    assert found.results[0].source == "journal"


def test_feeds_on_request_and_all(tmp_path) -> None:  # type: ignore[no-untyped-def]
    store = build_index(tmp_path)
    feeds = recall(store, "lantern", settings_for(["git", "vaults"]))
    assert len(feeds.results) == 2
    for c in feeds.results:
        assert c.trusted is False
        assert c.source != "journal"
    everything = recall(store, "lantern", settings_for(["journal", "git", "vaults"]))
    assert len(everything.results) == 3


def test_packed_line_names_the_source(tmp_path) -> None:  # type: ignore[no-untyped-def]
    store = build_index(tmp_path)
    feeds = recall(store, "lantern", settings_for(["git"]))
    packed = pack(feeds.results, 3000, 10, estimate_counter())
    assert "git commit: Ship the lantern" in packed.text
    journal = recall(store, "lantern", settings_for(["journal"]))
    plain = pack(journal.results, 3000, 10, estimate_counter())
    assert "idea: Ship the lantern" in plain.text


def candidate(text: str, source: str) -> Candidate:
    return Candidate(1, "x", "idea", text, "", "", source, source == "journal")


def candidates(prefix: str, count: int, source: str) -> list[Candidate]:
    return [candidate(prefix + str(i), source) for i in range(count)]


def test_merge_keeps_the_journal_first() -> None:
    # A full journal: the first 7 journal results, then 3 feed results, and no more.
    out = merge_reserved(candidates("journal ", 9, "journal"), candidates("feed ", 4, "git"), 10)
    assert len(out) == 10
    assert out[6].text == "journal 6"
    assert out[7].text == "feed 0"
    assert out[9].text == "feed 2"
    # A short journal: the feeds fill the rest, in their own order.
    filled = merge_reserved(candidates("journal ", 2, "journal"), candidates("feed ", 5, "git"), 10)
    assert len(filled) == 7
    assert filled[1].text == "journal 1"
    assert filled[2].text == "feed 0"
    assert filled[6].text == "feed 4"
    # A text that is in both lists is kept once, where the journal has it.
    deduped = merge_reserved(
        [candidate("shared text", "journal")],
        [candidate("Shared  text", "git"), candidate("other", "git")],
        10,
    )
    assert len(deduped) == 2
    assert deduped[0].source == "journal"
    assert deduped[1].text == "other"


def test_default_hints_at_feeds_when_the_journal_abstains(tmp_path) -> None:  # type: ignore[no-untyped-def]
    # "note" and "lantern" are in the vault section, and only "lantern" is in the journal, so the
    # journal abstains (one of three terms) and the feeds may match (two of three).
    store = build_index(tmp_path)
    question = "lantern note section"
    default = recall(store, question, settings_for(["journal"]))
    assert default.abstention.abstain
    assert default.feed_hint == 1
    for c in default.results:
        assert c.source == "journal"
    everything = recall(store, question, settings_for(["journal", "git", "vaults"]))
    assert everything.abstention.abstain
    assert everything.feed_hint == 0
    feeds = [c for c in everything.results if c.source != "journal"]
    assert len(feeds) > 0, "all shows the feed records after the journal's"
    for c in feeds:
        assert c.trusted is False
    assert everything.results[0].source == "journal"
