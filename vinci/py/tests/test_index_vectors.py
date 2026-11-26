"""The vector route end to end, without a server: vectors are seeded into a temporary embedding
cache, and the base URL is a closed port, so any request would fail. Covers the skip rule of
`index embed`, the one-label matrix, and fusion in recall."""

import array
import struct
from dataclasses import replace
from pathlib import Path

import pytest

from vinci_core.embed_config import EmbedConfig
from vinci_core.index_store import Fact, IndexStore
from vinci_core.recall import Settings, recall
from vinci_core.timeutil import parse_instant
from vinci_core.vector_scan import normalize
from vinci_core.vectors import embed_missing, floats_of, load_vectors
from vinci_core.embed_config import embed_question
from vinci_edge import embed as edge_embed

DEAD_URL = "http://127.0.0.1:9/v1"
LABEL = "test-label"
INSTRUCTION = "test instruction"
NOW = "2026-11-16T12:00:00+00:00"


def fact(record_id: str, version_hash: str, text: str, status: str) -> Fact:
    return Fact("journal", record_id, version_hash, "idea", text, record_id, "2026-11-01T10:00:00+00:00", None, [], [], True, status, 1)


def fixture_store() -> IndexStore:
    store = IndexStore(":memory:")
    store.put(fact("f1", "h1", "portal migration status", "active"))
    store.put(fact("f2", "h2", "unrelated grocery list", "active"))
    store.put(fact("f3", "h3", "plan for the offsite", "active"))
    # Python's sqlite3 opens a transaction on the first INSERT. Commit it, so the tests' own begin()
    # calls are the only transactions.
    store.conn.commit()
    return store


def seed_cache(path: str, label: str, texts: list[str], vectors: list[list[float]]) -> None:
    """Write vectors for exact texts into an embedding cache, under a label."""
    cache = edge_embed.EmbedCache(path)
    rows = [(edge_embed.cache_key(label, text), list(vec)) for text, vec in zip(texts, vectors)]
    cache.put_many(label, rows)
    cache.close()


def vec3(a: float, b: float, c: float) -> list[float]:
    return [a, b, c]


def put_vectors(store: IndexStore, label: str, rowids: list[int], vectors: list[list[float]]) -> None:
    store.begin()
    for rowid, v in zip(rowids, vectors):
        store.put_vector(rowid, label, struct.pack("<3f", v[0], v[1], v[2]), 3)
    store.commit()


def cfg(base_url: str, label: str, cache: str, instruction: str = INSTRUCTION) -> EmbedConfig:
    return EmbedConfig(base_url, "test-model", label, cache, instruction)


def test_embed_skips_facts_that_already_have_a_vector() -> None:
    store = fixture_store()
    assert len(store.missing_vectors(LABEL)) == 3
    put_vectors(store, LABEL, [2], [vec3(1, 0, 0)])
    missing = store.missing_vectors(LABEL)
    assert len(missing) == 2
    assert missing[0].text == "portal migration status"
    assert missing[1].text == "plan for the offsite"
    # Another label has none of them, so it still needs all three.
    assert len(store.missing_vectors("other-label")) == 3


def test_superseded_but_active_versions_still_need_vectors() -> None:
    store = fixture_store()
    # A new version of f1 ends the old one, which stays active for the temporal route.
    store.put(fact("f1", "h1b", "portal migration done", "active"))
    assert len(store.missing_vectors(LABEL)) == 4


def test_embed_writes_vectors_from_the_cache_and_asks_no_server(tmp_path: Path) -> None:
    cache = str(tmp_path / "cache.sqlite")
    store = fixture_store()
    texts = ["portal migration status", "unrelated grocery list", "plan for the offsite"]
    vectors = [vec3(0, 1, 0), vec3(0.9, 0.1, 0), vec3(0.8, 0, 0.6)]
    seed_cache(cache, LABEL, texts, vectors)
    embed_missing(store, cfg(DEAD_URL, LABEL, cache))
    assert len(store.missing_vectors(LABEL)) == 0
    assert load_vectors(store, LABEL, False).count() == 3


def test_recall_fuses_the_vector_and_lexical_routes(tmp_path: Path) -> None:
    cache = str(tmp_path / "cache.sqlite")
    store = fixture_store()
    texts = ["portal migration status", "unrelated grocery list", "plan for the offsite"]
    vectors = [vec3(0, 1, 0), vec3(0.9, 0.1, 0), vec3(0.8, 0, 0.6)]
    seed_cache(cache, LABEL, texts, vectors)
    # The question's own vector, under the query instruction.
    seed_cache(cache, LABEL, ["Instruct: " + INSTRUCTION + "\nQuery: portal migration plan"], [vec3(1, 0, 0)])
    put_vectors(store, LABEL, [1, 2, 3], vectors)
    settings = Settings(10, ["lexical", "vector"], parse_instant(NOW).utc, "2026-11-16", cfg(DEAD_URL, LABEL, cache))
    answer = recall(store, "portal migration plan", settings)
    # The semantic route's weights are lexical 0.25, vector 1.0. Fused: f1 = 0.25/61 + 1/63,
    # f3 = 0.25/62 + 1/62, f2 = 1/61 (vector rank 1 only). Route scores divide by the peak, f3.
    # Order: f3, f1, f2.
    assert [c.id for c in answer.results] == ["f3", "f1", "f2"]
    assert answer.results[2].in_vector is True
    assert answer.results[2].in_lexical is False
    assert answer.results[0].has_cosine
    assert abs(answer.results[0].cosine - 0.8) < 1e-5
    assert not answer.abstention.abstain
    assert not answer.corrected


def test_vector_route_without_vectors_uses_no_embedding() -> None:
    # No vectors for this label: the vector route is skipped, so the closed port is never reached,
    # and lexical results still come back.
    store = fixture_store()
    cfg_unembedded = cfg(DEAD_URL, "unembedded", "")
    answer = recall(store, "portal migration plan", Settings(10, ["lexical", "vector"], parse_instant(NOW).utc, "2026-11-16", cfg_unembedded))
    assert len(answer.results) == 2
    assert answer.results[0].id == "f1"


def test_embed_leaves_an_unembeddable_fact_out_and_counts_it(tmp_path: Path) -> None:
    """A text that is not cached, with the server unreachable, must not stop the run."""
    cache = str(tmp_path / "partial.sqlite")
    store = fixture_store()
    # Only two of the three texts are cached: "unrelated grocery list" is not.
    seed_cache(cache, LABEL, ["portal migration status", "plan for the offsite"], [vec3(0, 1, 0), vec3(0.8, 0, 0.6)])
    left_out = embed_missing(store, cfg(DEAD_URL, LABEL, cache))
    assert left_out == 1
    still = store.missing_vectors(LABEL)
    assert len(still) == 1
    assert still[0].text == "unrelated grocery list"
    assert load_vectors(store, LABEL, False).count() == 2


def test_query_lookup_order_main_then_fallback_then_miss(tmp_path: Path) -> None:
    """A question vector: main label in the cache, else the fallback label in the cache, else a call
    to the fallback URL (closed here, so a miss raises)."""
    cache = str(tmp_path / "queries.sqlite")
    seed_cache(cache, LABEL, ["Instruct: " + INSTRUCTION + "\nQuery: question on main"], [vec3(1, 0, 0)])
    seed_cache(cache, "fallback-label", ["Instruct: " + INSTRUCTION + "\nQuery: question on fallback"], [vec3(0, 0, 1)])
    base = cfg(DEAD_URL, LABEL, cache)
    with_fallback = replace(base, fallback_label="fallback-label", fallback_url=DEAD_URL)

    # 1. Under the main label: that vector, and the fallback URL is never called.
    main_hit = floats_of(embed_question(with_fallback, "question on main"))
    assert main_hit[0] == pytest.approx(1.0)
    assert main_hit[2] == pytest.approx(0.0)
    # 2. Not under the main label, but under the fallback label: that vector.
    fallback_hit = floats_of(embed_question(with_fallback, "question on fallback"))
    assert fallback_hit[2] == pytest.approx(1.0)
    # 3. In neither: the closed fallback URL is called and refuses, so the call raises.
    with pytest.raises(Exception):
        embed_question(with_fallback, "question nowhere")


def test_loaded_rows_are_the_stored_vectors(tmp_path: Path) -> None:
    """Every loaded row equals its stored vector, normalised, at the production width of 2560
    dimensions, on two loads in a row. This guards the copy in load_vectors."""
    dims = 2560
    rows = 463
    store = IndexStore(str(tmp_path / "vectors.sqlite"))
    expected: list[list[float]] = []
    store.begin()
    for r in range(rows):
        store.put(fact("v" + str(r), "h" + str(r), "vector " + str(r), "active"))
        raw = [float(((r * 31 + j * 17) % 97) - 48) / 50 for j in range(dims)]
        blob = array.array("f", raw).tobytes()
        store.put_vector(r + 1, LABEL, blob, dims)
        expected.append(normalize(raw))
    store.commit()
    store.conn.commit()
    # Two loads in a row, as the chat's second recall does; each must match.
    for _ in range(2):
        index = load_vectors(store, LABEL, False)
        assert index.count() == rows
        bad = 0
        for r in range(rows):
            for j in range(dims):
                if abs(index.matrix.data[r * dims + j] - expected[r][j]) > 1e-5:
                    bad += 1
        assert bad == 0
