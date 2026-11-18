# Recall tests: the tokeniser (Retrieval.cs Terms and LexicalQuery), FTS5 syntax in
# queries, the current-and-active filter, and the route refusals step 2b relies on.

from std.testing import assert_equal, assert_false, assert_true

from index_store import Fact, IndexStore
from embed_edge import EmbedConfig
from recall import (
    Settings,
    fts_query,
    lexical,
    query_terms,
    quote_phrase,
    recall,
)


def fact(
    record_id: String,
    version_hash: String,
    kind: String,
    text: String,
    at: String,
    trusted: Bool,
    status: String,
) raises -> Fact:
    var tags = List[String]()
    var refs = List[String]()
    return Fact(
        "journal",
        record_id,
        version_hash,
        kind,
        text,
        record_id,
        at,
        None,
        tags^,
        refs^,
        trusted,
        status,
        1,
    )


def expect_terms(query: String, expected: List[String]) raises:
    var got = query_terms(query)
    assert_equal(len(got), len(expected))
    for i in range(len(expected)):
        assert_equal(got[i], expected[i])


def test_terms_follow_retrieval_cs() raises:
    # "what", "did", "about", "the" are stop words (Retrieval.cs StopWords); "we" is
    # two bytes, so it is dropped as well.
    var expected = List[String]()
    expected.append("decide")
    expected.append("gomath")
    expected.append("bank")
    expect_terms("What did we decide about the GoMath bank?", expected)


def test_terms_keep_hyphens_and_underscores() raises:
    var expected = List[String]()
    expected.append("auto-indexed")
    expected.append("foo_bar")
    expect_terms("auto-indexed foo_bar x1", expected)


def test_query_with_no_terms_falls_back_to_its_words() raises:
    var empty = List[String]()
    expect_terms("a I", empty)
    assert_equal(fts_query("a I"), '"a" OR "I"')
    assert_equal(fts_query("   "), "")
    assert_equal(fts_query("alpha beta"), '"alpha" OR "beta"')


def test_quote_phrase_doubles_quotes() raises:
    assert_equal(quote_phrase('a"b'), '"a""b"')
    assert_equal(quote_phrase("plain"), '"plain"')


def test_fts5_syntax_in_queries_is_text() raises:
    var store = IndexStore(":memory:")
    _ = store.put(
        fact(
            "r1",
            "h1",
            "idea",
            "alpha NOT beta",
            "2026-01-01T00:00:00Z",
            True,
            "active",
        )
    )
    _ = store.put(
        fact(
            "r2",
            "h2",
            "idea",
            'quoted "text" here',
            "2026-01-01T00:00:00Z",
            True,
            "active",
        )
    )
    var queries = List[String]()
    queries.append('"')
    queries.append('"unbalanced')
    queries.append("NOT")
    queries.append("alpha NOT")
    queries.append("alpha*")
    queries.append("title:alpha")
    queries.append("(alpha OR beta)")
    queries.append("NEAR(alpha beta)")
    queries.append("-alpha")
    queries.append("^alpha")
    queries.append("a\\b")
    queries.append('he said "hi" ok')
    queries.append("   ")
    for q in queries:
        # Each query runs without raising, whatever FTS5 would make of it as syntax.
        _ = lexical(store, q, 10, False)
    # Quoted, "NOT" is an ordinary word, so it finds only what contains it.
    var words = lexical(store, "NOT", 10, False)
    assert_equal(len(words), 1)
    assert_equal(words[0].id, "r1")
    # A quote inside a query is doubled, so the phrase still matches the text.
    var quoted = lexical(store, 'quoted "text"', 10, False)
    assert_equal(len(quoted), 1)
    assert_equal(quoted[0].id, "r2")


def test_superseded_and_inactive_versions_are_not_returned() raises:
    var store = IndexStore(":memory:")
    _ = store.put(
        fact(
            "r1",
            "hA",
            "memory",
            "alpha beta",
            "2026-01-01T00:00:00Z",
            True,
            "active",
        )
    )
    _ = store.put(
        fact(
            "r1",
            "hB",
            "memory",
            "gamma delta",
            "2026-02-01T00:00:00Z",
            True,
            "active",
        )
    )
    _ = store.put(
        fact(
            "r2",
            "hC",
            "memory",
            "alpha retracted",
            "2026-01-05T00:00:00Z",
            True,
            "retracted",
        )
    )
    _ = store.put(
        fact(
            "r3",
            "hD",
            "memory",
            "alpha current",
            "2026-01-06T00:00:00Z",
            True,
            "active",
        )
    )
    # The old version of r1 still exists, but it is not current.
    assert_equal(store.fact_total(), 4)
    var alpha = lexical(store, "alpha", 10, False)
    assert_equal(len(alpha), 1)
    assert_equal(alpha[0].id, "r3")
    var gamma = lexical(store, "gamma", 10, False)
    assert_equal(len(gamma), 1)
    assert_equal(gamma[0].id, "r1")


def test_result_holds_the_citation_and_the_text() raises:
    var store = IndexStore(":memory:")
    _ = store.put(
        fact(
            "m12",
            "h1",
            "memory",
            "Bun runtime notes",
            "2026-03-04T05:06:07Z",
            False,
            "active",
        )
    )
    var found = lexical(store, "bun", 5, False)
    assert_equal(len(found), 1)
    var r = found[0].copy()
    assert_equal(r.id, "m12")
    assert_equal(r.kind, "memory")
    assert_equal(r.text, "Bun runtime notes")
    assert_equal(r.at, "2026-03-04T05:06:07Z")
    assert_equal(r.source, "journal")
    assert_false(r.trusted)
    # The raw route has no fused score yet: fusion sets it.
    assert_equal(r.fused, 0.0)
    assert_true(r.in_lexical and not r.in_vector)


def test_or_matching_and_limit() raises:
    var store = IndexStore(":memory:")
    _ = store.put(
        fact("a", "h1", "idea", "alpha", "2026-01-01T00:00:00Z", True, "active")
    )
    _ = store.put(
        fact("b", "h2", "idea", "beta", "2026-01-01T00:00:00Z", True, "active")
    )
    _ = store.put(
        fact("c", "h3", "idea", "gamma", "2026-01-01T00:00:00Z", True, "active")
    )
    # Words are joined with OR, as Neo4j's full-text index joins them.
    assert_equal(len(lexical(store, "alpha beta", 10, False)), 2)
    assert_equal(len(lexical(store, "alpha beta gamma", 2, False)), 2)
    assert_equal(len(lexical(store, "alpha", 0, False)), 0)


def test_recall_refuses_unknown_and_empty_routes() raises:
    var store = IndexStore(":memory:")
    _ = store.put(
        fact(
            "r1",
            "h1",
            "idea",
            "alpha beta",
            "2026-01-01T00:00:00Z",
            True,
            "active",
        )
    )
    var none = List[String]()
    var settings_none = Settings(
        5, none^, 0, "2026-11-16", EmbedConfig("", "", "", "", "")
    )
    var refused = False
    try:
        _ = recall(store, "alpha", settings_none)
    except:
        refused = True
    assert_true(refused)
    var graph = List[String]()
    graph.append("graph")
    var settings_graph = Settings(
        5, graph^, 0, "2026-11-16", EmbedConfig("", "", "", "", "")
    )
    refused = False
    try:
        _ = recall(store, "alpha", settings_graph)
    except:
        refused = True
    assert_true(refused)


def test_recall_lexical_only_answers_without_a_vector_store() raises:
    var store = IndexStore(":memory:")
    _ = store.put(
        fact(
            "r1",
            "h1",
            "idea",
            "alpha beta",
            "2026-01-01T00:00:00Z",
            True,
            "active",
        )
    )
    var routes = List[String]()
    routes.append("lexical")
    # The vector route is not asked for, so no embedding is made (base URL unused).
    var settings = Settings(
        5,
        routes^,
        0,
        "2026-11-16",
        EmbedConfig("http://127.0.0.1:9/v1", "m", "m", "", ""),
    )
    var answer = recall(store, "alpha", settings)
    assert_equal(len(answer.results), 1)
    assert_equal(answer.results[0].id, "r1")
    assert_false(answer.abstention.abstain)
    assert_equal(answer.route, "semantic")
    assert_equal(answer.complexity, "simple")


def main() raises:
    test_terms_follow_retrieval_cs()
    print("test_terms_follow_retrieval_cs: ok")
    test_terms_keep_hyphens_and_underscores()
    print("test_terms_keep_hyphens_and_underscores: ok")
    test_query_with_no_terms_falls_back_to_its_words()
    print("test_query_with_no_terms_falls_back_to_its_words: ok")
    test_quote_phrase_doubles_quotes()
    print("test_quote_phrase_doubles_quotes: ok")
    test_fts5_syntax_in_queries_is_text()
    print("test_fts5_syntax_in_queries_is_text: ok")
    test_superseded_and_inactive_versions_are_not_returned()
    print("test_superseded_and_inactive_versions_are_not_returned: ok")
    test_result_holds_the_citation_and_the_text()
    print("test_result_holds_the_citation_and_the_text: ok")
    test_or_matching_and_limit()
    print("test_or_matching_and_limit: ok")
    test_recall_refuses_unknown_and_empty_routes()
    print("test_recall_refuses_unknown_and_empty_routes: ok")
    test_recall_lexical_only_answers_without_a_vector_store()
    print("test_recall_lexical_only_answers_without_a_vector_store: ok")
