# Recall by source (M5 step a2): parse_sources, the journal and feed search tables kept apart,
# the default of journal only, the feed sources on request, and the source label in the packed
# text. A temporary index in each test; the real index is never opened.

from std.python import Python, PythonObject
from std.testing import assert_equal, assert_false, assert_true

from embed_edge import default_config
from feed_store import feed_record, put_record
from index_store import Fact, IndexStore, py_params
from pack import estimate_counter, pack
from recall import Settings, merge_reserved, parse_sources, recall
from fusion import Candidate


def temp_dir() raises -> String:
    var tempfile = Python.import_module("tempfile")
    return String(tempfile.mkdtemp())


def journal_fact(mut store: IndexStore, cite: String, text: String) raises:
    store.begin()
    var empty = List[String]()
    _ = store.put(
        Fact(
            "journal",
            "idea:" + cite,
            "hash-" + cite,
            "idea",
            text,
            cite,
            "2026-11-10T09:00:00-06:00",
            None,
            empty.copy(),
            empty^,
            True,
            "active",
            1,
        )
    )
    store.commit()


def settings_for(sources: List[String]) raises -> Settings:
    var routes = List[String]()
    routes.append("lexical")
    var settings = Settings(10, routes^, 0, "2026-11-18", default_config())
    settings.sources = sources.copy()
    return settings^


def names(*items: String) -> List[String]:
    var out = List[String]()
    for item in items:
        out.append(String(item))
    return out^


def build_index() raises -> IndexStore:
    var store = IndexStore(temp_dir() + "/index.sqlite")
    journal_fact(store, "i1", "Ship the lantern prototype this week")
    store.begin()
    _ = put_record(
        store,
        feed_record(
            "git",
            "abc1234567",
            "2026-11-17T10:00:00+00:00",
            "commit",
            "Ship the lantern",
            "",
            "Ship the lantern\n\ncommit abc1234567\nChanged 1 files, +1 -0\n",
            List[String](),
        ),
    )
    _ = put_record(
        store,
        feed_record(
            "vaults",
            "Notes/lantern.md#lantern",
            "2026-11-16T10:00:00+00:00",
            "section",
            "lantern",
            "/x/Notes/lantern.md",
            "lantern\n\n# Lantern\n\nThe lantern is a note.\n",
            List[String](),
        ),
    )
    store.commit()
    return store^


def test_parse_sources() raises:
    assert_equal(len(parse_sources("all")), 3)
    var pair = parse_sources("git,vaults")
    assert_equal(len(pair), 2)
    assert_equal(pair[0], "git")
    assert_equal(len(parse_sources("journal")), 1)
    var refused = False
    try:
        _ = parse_sources("journal,bogus")
    except:
        refused = True
    assert_true(refused, "an unknown source must be refused")
    # activity is named on its own: all stays git, vaults and the journal (M5 step b).
    assert_equal(len(parse_sources("all")), 3)
    var activity = parse_sources("activity,git")
    assert_equal(len(activity), 2)
    assert_equal(activity[0], "activity")


def test_feed_records_stay_out_of_the_journal_table() raises:
    var store = build_index()
    var rows = store.conn.execute(
        "SELECT COUNT(*) FROM fts JOIN facts AS f ON f.rowid = fts.rowid"
        " WHERE f.source <> 'journal'"
    ).fetchone()
    assert_equal(Int(String(rows[0])), 0)
    var feed_rows = store.conn.execute(
        "SELECT COUNT(*) FROM fts_feeds"
    ).fetchone()
    assert_equal(Int(String(feed_rows[0])), 2)


def test_default_is_journal_only() raises:
    var store = build_index()
    var found = recall(store, "lantern", settings_for(names("journal")))
    assert_equal(len(found.results), 1)
    assert_equal(found.results[0].source, "journal")
    assert_false(
        found.results[0].trusted and found.results[0].source != "journal"
    )


def test_feeds_on_request_and_all() raises:
    var store = build_index()
    var feeds = recall(store, "lantern", settings_for(names("git", "vaults")))
    assert_equal(len(feeds.results), 2)
    for c in feeds.results:
        assert_equal(c.trusted, False)
        assert_false(c.source == "journal")
    var everything = recall(
        store, "lantern", settings_for(names("journal", "git", "vaults"))
    )
    assert_equal(len(everything.results), 3)


def test_packed_line_names_the_source() raises:
    var store = build_index()
    var feeds = recall(store, "lantern", settings_for(names("git")))
    var packed = pack(feeds.results, 3000, 10, estimate_counter())
    assert_true(packed.text.find("git commit: Ship the lantern") >= 0)
    var journal = recall(store, "lantern", settings_for(names("journal")))
    var plain = pack(journal.results, 3000, 10, estimate_counter())
    assert_true(plain.text.find("idea: Ship the lantern") >= 0)


def candidate(text: String, source: String) -> Candidate:
    return Candidate(
        1,
        String("x"),
        String("idea"),
        text,
        String(""),
        String(""),
        source,
        source == "journal",
    )


def candidates(prefix: String, count: Int, source: String) -> List[Candidate]:
    var out = List[Candidate]()
    for i in range(count):
        out.append(candidate(prefix + String(i), source))
    return out^


def test_merge_keeps_the_journal_first() raises:
    # A full journal: the first 7 journal results, then 3 feed results, and no more.
    var journal = candidates("journal ", 9, "journal")
    var feeds = candidates("feed ", 4, "git")
    var out = merge_reserved(journal, feeds, 10)
    assert_equal(len(out), 10)
    assert_equal(out[6].text, "journal 6")
    assert_equal(out[7].text, "feed 0")
    assert_equal(out[9].text, "feed 2")
    # A short journal: the feeds fill the rest, in their own order.
    var short = candidates("journal ", 2, "journal")
    var filled = merge_reserved(short, candidates("feed ", 5, "git"), 10)
    assert_equal(len(filled), 7)
    assert_equal(filled[1].text, "journal 1")
    assert_equal(filled[2].text, "feed 0")
    assert_equal(filled[6].text, "feed 4")
    # A text that is in both lists is kept once, where the journal has it.
    var same = List[Candidate]()
    same.append(candidate("shared text", "journal"))
    var again = List[Candidate]()
    again.append(candidate("Shared  text", "git"))
    again.append(candidate("other", "git"))
    var deduped = merge_reserved(same, again, 10)
    assert_equal(len(deduped), 2)
    assert_equal(deduped[0].source, "journal")
    assert_equal(deduped[1].text, "other")


def test_default_hints_at_feeds_when_the_journal_abstains() raises:
    # "note" and "lantern" are in the vault section, and only "lantern" is in the journal, so
    # the journal abstains (one of three terms) and the feeds may match (two of three).
    var store = build_index()
    var question = "lantern note section"
    var default = recall(store, question, settings_for(names("journal")))
    assert_true(default.abstention.abstain)
    assert_equal(default.feed_hint, 1)
    for c in default.results:
        assert_equal(c.source, "journal")
    var everything = recall(
        store, question, settings_for(names("journal", "git", "vaults"))
    )
    assert_true(everything.abstention.abstain)
    assert_equal(everything.feed_hint, 0)
    var has_feed = False
    for c in everything.results:
        if c.source != "journal":
            has_feed = True
            assert_equal(c.trusted, False)
    assert_true(has_feed, "all shows the feed records after the journal's")
    assert_equal(everything.results[0].source, "journal")


def main() raises:
    test_parse_sources()
    print("test_parse_sources: ok")
    test_feed_records_stay_out_of_the_journal_table()
    print("test_feed_records_stay_out_of_the_journal_table: ok")
    test_default_is_journal_only()
    print("test_default_is_journal_only: ok")
    test_feeds_on_request_and_all()
    print("test_feeds_on_request_and_all: ok")
    test_packed_line_names_the_source()
    print("test_packed_line_names_the_source: ok")
    test_merge_keeps_the_journal_first()
    print("test_merge_keeps_the_journal_first: ok")
    test_default_hints_at_feeds_when_the_journal_abstains()
    print("test_default_hints_at_feeds_when_the_journal_abstains: ok")
