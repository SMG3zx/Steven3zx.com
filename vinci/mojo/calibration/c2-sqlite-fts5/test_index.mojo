from std.testing import assert_equal, assert_true

from index import Hit, Index


def test_idempotent_readd() raises:
    var idx = Index(":memory:")
    idx.add("journal", "1", "t", "idea", "alpha", "alpha body", "h1")
    idx.add("journal", "1", "t", "idea", "alpha", "alpha body", "h1")
    assert_equal(len(idx.search("alpha", 10)), 1)
    # Same (source, id, hash) with different text changes nothing.
    idx.add("journal", "1", "t", "idea", "gamma", "gamma body", "h1")
    assert_equal(len(idx.search("gamma", 10)), 0)
    assert_equal(len(idx.search("alpha", 10)), 1)


def test_new_version_replaces_old() raises:
    var idx = Index(":memory:")
    idx.add("journal", "7", "t", "idea", "alpha", "alpha body", "h1")
    idx.add("journal", "7", "t", "idea", "beta", "beta body", "h2")
    assert_equal(len(idx.search("alpha", 10)), 0)
    var hits = idx.search("beta", 10)
    assert_equal(len(hits), 1)
    assert_equal(hits[0].id, "7")


def test_bm25_ordering() raises:
    var idx = Index(":memory:")
    idx.add(
        "journal", "a", "t", "k", "sqlite", "sqlite sqlite sqlite fts5", "1"
    )
    idx.add("journal", "b", "t", "k", "notes", "sqlite", "2")
    idx.add(
        "journal",
        "c",
        "t",
        "k",
        "x",
        (
            "a long note about many other things that mention sqlite once among"
            " many words here and there"
        ),
        "3",
    )
    idx.add("journal", "d", "t", "k", "cats", "dogs and birds", "4")
    idx.add("journal", "e", "t", "k", "sqlite sqlite", "sqlite", "5")
    var hits = idx.search("sqlite", 10)
    assert_equal(len(hits), 4)
    for i in range(len(hits) - 1):
        assert_true(hits[i].score <= hits[i + 1].score)
    assert_true(hits[0].id == "a" or hits[0].id == "e")
    assert_equal(hits[3].id, "c")
    for hit in hits:
        assert_true(hit.id != "d")


def test_k_limit() raises:
    var idx = Index(":memory:")
    for i in range(5):
        idx.add("journal", String(i), "t", "k", "sqlite", "sqlite", String(i))
    assert_equal(len(idx.search("sqlite", 2)), 2)
    assert_equal(len(idx.search("sqlite", 10)), 5)
    assert_equal(len(idx.search("sqlite", 0)), 0)


def test_no_hits() raises:
    var idx = Index(":memory:")
    idx.add("journal", "1", "t", "k", "alpha", "alpha body", "h1")
    assert_equal(len(idx.search("zzqqxx", 10)), 0)
    assert_equal(len(idx.search("", 10)), 0)


def test_fts_syntax_in_query() raises:
    var idx = Index(":memory:")
    idx.add("journal", "1", "t", "k", "sqlite", "sqlite fts5 notes", "h1")
    assert_true(len(idx.search('"sqlite', 10)) >= 1)
    _ = idx.search("sqlite-fts", 10)
    _ = idx.search('foo"bar AND (x) NOT y * :', 10)
    _ = idx.search('"', 10)
    _ = idx.search("title:sqlite", 10)
    assert_equal(len(idx.search("NOT", 10)), 0)


def main() raises:
    test_idempotent_readd()
    test_new_version_replaces_old()
    test_bm25_ordering()
    test_k_limit()
    test_no_hits()
    test_fts_syntax_in_query()
    print("test_index: ok")
