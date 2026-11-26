"""Recall tests: the tokeniser (Retrieval.cs Terms and LexicalQuery), FTS5 syntax in queries, the
current-and-active filter, and the route refusals step 2b relies on."""

import pytest

from vinci_core.embed_config import EmbedConfig
from vinci_core.index_store import Fact, IndexStore
from vinci_core.recall import Settings, lexical, recall
from vinci_core.terms import fts_query, query_terms, quote_phrase


def fact(record_id: str, version_hash: str, kind: str, text: str, at: str, trusted: bool, status: str) -> Fact:
    return Fact("journal", record_id, version_hash, kind, text, record_id, at, None, [], [], trusted, status, 1)


def test_terms_follow_retrieval_cs() -> None:
    # "what", "did", "about", "the" are stop words; "we" is two bytes, so it is dropped as well.
    assert query_terms("What did we decide about the GoMath bank?") == ["decide", "gomath", "bank"]


def test_terms_keep_hyphens_and_underscores() -> None:
    assert query_terms("auto-indexed foo_bar x1") == ["auto-indexed", "foo_bar"]


def test_query_with_no_terms_falls_back_to_its_words() -> None:
    assert query_terms("a I") == []
    assert fts_query("a I") == '"a" OR "I"'
    assert fts_query("   ") == ""
    assert fts_query("alpha beta") == '"alpha" OR "beta"'


def test_quote_phrase_doubles_quotes() -> None:
    assert quote_phrase('a"b') == '"a""b"'
    assert quote_phrase("plain") == '"plain"'


def test_fts5_syntax_in_queries_is_text() -> None:
    store = IndexStore(":memory:")
    store.put(fact("r1", "h1", "idea", "alpha NOT beta", "2026-01-01T00:00:00Z", True, "active"))
    store.put(fact("r2", "h2", "idea", 'quoted "text" here', "2026-01-01T00:00:00Z", True, "active"))
    queries = [
        '"', '"unbalanced', "NOT", "alpha NOT", "alpha*", "title:alpha", "(alpha OR beta)",
        "NEAR(alpha beta)", "-alpha", "^alpha", "a\\b", 'he said "hi" ok', "   ",
    ]
    for q in queries:
        # Each query runs without raising, whatever FTS5 would make of it as syntax.
        lexical(store, q, 10, False)
    # Quoted, "NOT" is an ordinary word, so it finds only what contains it.
    words = lexical(store, "NOT", 10, False)
    assert len(words) == 1
    assert words[0].id == "r1"
    # A quote inside a query is doubled, so the phrase still matches the text.
    quoted = lexical(store, 'quoted "text"', 10, False)
    assert len(quoted) == 1
    assert quoted[0].id == "r2"


def test_superseded_and_inactive_versions_are_not_returned() -> None:
    store = IndexStore(":memory:")
    store.put(fact("r1", "hA", "memory", "alpha beta", "2026-01-01T00:00:00Z", True, "active"))
    store.put(fact("r1", "hB", "memory", "gamma delta", "2026-02-01T00:00:00Z", True, "active"))
    store.put(fact("r2", "hC", "memory", "alpha retracted", "2026-01-05T00:00:00Z", True, "retracted"))
    store.put(fact("r3", "hD", "memory", "alpha current", "2026-01-06T00:00:00Z", True, "active"))
    # The old version of r1 still exists, but it is not current.
    assert store.fact_total() == 4
    alpha = lexical(store, "alpha", 10, False)
    assert len(alpha) == 1
    assert alpha[0].id == "r3"
    gamma = lexical(store, "gamma", 10, False)
    assert len(gamma) == 1
    assert gamma[0].id == "r1"


def test_result_holds_the_citation_and_the_text() -> None:
    store = IndexStore(":memory:")
    store.put(fact("m12", "h1", "memory", "Bun runtime notes", "2026-03-04T05:06:07Z", False, "active"))
    found = lexical(store, "bun", 5, False)
    assert len(found) == 1
    r = found[0]
    assert r.id == "m12"
    assert r.kind == "memory"
    assert r.text == "Bun runtime notes"
    assert r.at == "2026-03-04T05:06:07Z"
    assert r.source == "journal"
    assert not r.trusted
    # The raw route has no fused score yet: fusion sets it.
    assert r.fused == 0.0
    assert r.in_lexical and not r.in_vector


def test_or_matching_and_limit() -> None:
    store = IndexStore(":memory:")
    store.put(fact("a", "h1", "idea", "alpha", "2026-01-01T00:00:00Z", True, "active"))
    store.put(fact("b", "h2", "idea", "beta", "2026-01-01T00:00:00Z", True, "active"))
    store.put(fact("c", "h3", "idea", "gamma", "2026-01-01T00:00:00Z", True, "active"))
    # Words are joined with OR.
    assert len(lexical(store, "alpha beta", 10, False)) == 2
    assert len(lexical(store, "alpha beta gamma", 2, False)) == 2
    assert len(lexical(store, "alpha", 0, False)) == 0


def test_recall_refuses_unknown_and_empty_routes() -> None:
    store = IndexStore(":memory:")
    store.put(fact("r1", "h1", "idea", "alpha beta", "2026-01-01T00:00:00Z", True, "active"))
    embed = EmbedConfig("", "", "", "", "")
    with pytest.raises(ValueError):
        recall(store, "alpha", Settings(5, [], 0, "2026-11-16", embed))
    with pytest.raises(ValueError):
        recall(store, "alpha", Settings(5, ["graph"], 0, "2026-11-16", embed))


def test_recall_lexical_only_answers_without_a_vector_store() -> None:
    store = IndexStore(":memory:")
    store.put(fact("r1", "h1", "idea", "alpha beta", "2026-01-01T00:00:00Z", True, "active"))
    # The vector route is not asked for, so no embedding is made (base URL unused).
    settings = Settings(5, ["lexical"], 0, "2026-11-16", EmbedConfig("http://127.0.0.1:9/v1", "m", "m", "", ""))
    answer = recall(store, "alpha", settings)
    assert len(answer.results) == 1
    assert answer.results[0].id == "r1"
    assert not answer.abstention.abstain
    assert answer.route == "semantic"
    assert answer.complexity == "simple"
