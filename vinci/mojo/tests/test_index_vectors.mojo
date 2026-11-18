# The vector route end to end, without a server: vectors are seeded into a temporary
# embedding cache, and the base URL is a closed port, so any request would fail.
# Covers the skip rule of `index embed`, the one-label matrix, and fusion in recall.

from std.python import Python, PythonObject
from std.testing import assert_equal, assert_false, assert_true

from embed_edge import EmbedConfig, embed_documents
from index_store import Fact, IndexStore, py_params
from recall import Settings, recall
from timeutil import parse_instant
from embed_edge import embed_question
from vectors import embed_missing, floats_of, load_vectors
from vector_scan import normalize

comptime DEAD_URL = "http://127.0.0.1:9/v1"
comptime LABEL = "test-label"
comptime INSTRUCTION = "test instruction"
comptime NOW = "2026-11-16T12:00:00+00:00"


def fact(
    record_id: String,
    version_hash: String,
    text: String,
    status: String,
) raises -> Fact:
    var tags = List[String]()
    var refs = List[String]()
    return Fact(
        "journal",
        record_id,
        version_hash,
        "idea",
        text,
        record_id,
        "2026-11-01T10:00:00+00:00",
        None,
        tags^,
        refs^,
        True,
        status,
        1,
    )


def fixture_store() raises -> IndexStore:
    var store = IndexStore(":memory:")
    _ = store.put(fact("f1", "h1", "portal migration status", "active"))
    _ = store.put(fact("f2", "h2", "unrelated grocery list", "active"))
    _ = store.put(fact("f3", "h3", "plan for the offsite", "active"))
    # Python's sqlite3 opens a transaction on the first INSERT. Commit it, so the
    # tests' own begin() calls are the only transactions (IndexStore.begin is BEGIN).
    store.conn.commit()
    return store^


def seed_cache(
    path: String,
    label: String,
    texts: List[String],
    vectors: List[List[Float32]],
) raises:
    """Write vectors for exact texts into an embedding cache, under a label."""
    var embed = Python.import_module("vinci_edge.embed")
    var cache = embed.EmbedCache(PythonObject(path))
    var rows = Python.list()
    for i in range(len(texts)):
        var vec = Python.list()
        for x in vectors[i]:
            vec.append(PythonObject(Float64(x)))
        var pair = Python.list()
        pair.append(
            embed.cache_key(PythonObject(label), PythonObject(texts[i]))
        )
        pair.append(vec)
        rows.append(pair)
    cache.put_many(PythonObject(label), rows)
    cache.close()


def vec3(a: Float32, b: Float32, c: Float32) -> List[Float32]:
    var v = List[Float32]()
    v.append(a)
    v.append(b)
    v.append(c)
    return v^


def put_vectors(
    mut store: IndexStore,
    label: String,
    rowids: List[Int],
    vectors: List[List[Float32]],
) raises:
    var struct_ = Python.import_module("struct")
    store.begin()
    for i in range(len(rowids)):
        var v = vectors[i].copy()
        var blob = struct_.pack("<3f", v[0], v[1], v[2])
        store.put_vector(rowids[i], label, blob, 3)
    store.commit()


def temp_dir() raises -> String:
    var tempfile = Python.import_module("tempfile")
    return String(tempfile.mkdtemp())


def set_py_path() raises:
    """Point vinci_edge at mojo/py, the directory the tests run from."""
    var os = Python.import_module("os")
    var py_dir = os.path.abspath(PythonObject("py"))
    os.environ[PythonObject("VINCI_MOJO_PY")] = py_dir
    var sys = Python.import_module("sys")
    sys.path.insert(0, py_dir)


def test_embed_skips_facts_that_already_have_a_vector() raises:
    var store = fixture_store()
    assert_equal(len(store.missing_vectors(LABEL)), 3)
    var rowids = List[Int]()
    rowids.append(2)
    var vectors = List[List[Float32]]()
    vectors.append(vec3(1, 0, 0))
    put_vectors(store, LABEL, rowids, vectors)
    var missing = store.missing_vectors(LABEL)
    assert_equal(len(missing), 2)
    assert_equal(missing[0].text, "portal migration status")
    assert_equal(missing[1].text, "plan for the offsite")
    # Another label has none of them, so it still needs all three.
    assert_equal(len(store.missing_vectors("other-label")), 3)


def test_superseded_but_active_versions_still_need_vectors() raises:
    var store = fixture_store()
    # A new version of f1 ends the old one, which stays active for the temporal route.
    _ = store.put(fact("f1", "h1b", "portal migration done", "active"))
    var missing = store.missing_vectors(LABEL)
    assert_equal(len(missing), 4)


def test_embed_writes_vectors_from_the_cache_and_asks_no_server() raises:
    set_py_path()
    var dir = temp_dir()
    var cache = dir + "/cache.sqlite"
    var store = fixture_store()
    var texts = List[String]()
    texts.append("portal migration status")
    texts.append("unrelated grocery list")
    texts.append("plan for the offsite")
    var vectors = List[List[Float32]]()
    vectors.append(vec3(0, 1, 0))
    vectors.append(vec3(0.9, 0.1, 0))
    vectors.append(vec3(0.8, 0, 0.6))
    seed_cache(cache, LABEL, texts, vectors)
    var cfg = EmbedConfig(DEAD_URL, "test-model", LABEL, cache, INSTRUCTION)
    embed_missing(store, cfg)
    assert_equal(len(store.missing_vectors(LABEL)), 0)
    var index = load_vectors(store, LABEL, False)
    assert_equal(index.count(), 3)


def test_recall_fuses_the_vector_and_lexical_routes() raises:
    set_py_path()
    var dir = temp_dir()
    var cache = dir + "/cache.sqlite"
    var store = fixture_store()
    var texts = List[String]()
    texts.append("portal migration status")
    texts.append("unrelated grocery list")
    texts.append("plan for the offsite")
    var vectors = List[List[Float32]]()
    vectors.append(vec3(0, 1, 0))
    vectors.append(vec3(0.9, 0.1, 0))
    vectors.append(vec3(0.8, 0, 0.6))
    seed_cache(cache, LABEL, texts, vectors)
    # The question's own vector, under the query instruction.
    var query_text = (
        "Instruct: " + INSTRUCTION + "\nQuery: portal migration plan"
    )
    var question = List[String]()
    question.append(query_text)
    var query_vectors = List[List[Float32]]()
    query_vectors.append(vec3(1, 0, 0))
    seed_cache(cache, LABEL, question, query_vectors)
    var rowids = List[Int]()
    rowids.append(1)
    rowids.append(2)
    rowids.append(3)
    put_vectors(store, LABEL, rowids, vectors)

    var routes = List[String]()
    routes.append("lexical")
    routes.append("vector")
    var cfg = EmbedConfig(DEAD_URL, "test-model", LABEL, cache, INSTRUCTION)
    var now = parse_instant(NOW).utc
    var settings = Settings(10, routes^, now, "2026-11-16", cfg^)
    var answer = recall(store, "portal migration plan", settings)
    # The semantic route's weights are lexical 0.25, vector 1.0 (the default, M5 step d).
    # Fused: f1 = 0.25/61 + 1/63 = 0.0199714 (lexical rank 1, vector rank 3), f3 = 0.25/62
    # + 1/62 = 0.0201613 (lexical rank 2, vector rank 2), f2 = 1/61 = 0.0163934 (vector
    # rank 1 only). Route scores divide by the peak, f3: 1.0, f1: 0.99058, and f2 gets the
    # method boost: 0.81319 + 0.1 = 0.91319. Order: f3, f1, f2.
    assert_equal(len(answer.results), 3)
    assert_equal(answer.results[0].id, "f3")
    assert_equal(answer.results[1].id, "f1")
    assert_equal(answer.results[2].id, "f2")
    assert_equal(answer.results[2].in_vector, True)
    assert_equal(answer.results[2].in_lexical, False)
    assert_true(answer.results[0].has_cosine)
    var diff = answer.results[0].cosine - 0.8
    if diff < 0:
        diff = -diff
    assert_true(diff < 1e-5)
    assert_false(answer.abstention.abstain)
    assert_false(answer.corrected)


def test_vector_route_without_vectors_uses_no_embedding() raises:
    # No vectors for this label: the vector route is skipped, so the closed port is
    # never reached, and lexical results still come back.
    var store = fixture_store()
    var routes = List[String]()
    routes.append("lexical")
    routes.append("vector")
    var cfg = EmbedConfig(DEAD_URL, "test-model", "unembedded", "", INSTRUCTION)
    var answer = recall(
        store,
        "portal migration plan",
        Settings(10, routes^, parse_instant(NOW).utc, "2026-11-16", cfg^),
    )
    assert_equal(len(answer.results), 2)
    assert_equal(answer.results[0].id, "f1")


def test_embed_leaves_an_unembeddable_fact_out_and_counts_it() raises:
    """A text that is not cached, with the server unreachable, must not stop the run.
    """
    set_py_path()
    var dir = temp_dir()
    var cache = dir + "/partial.sqlite"
    var store = fixture_store()
    # Only two of the three texts are cached: "unrelated grocery list" is not.
    var texts = List[String]()
    texts.append("portal migration status")
    texts.append("plan for the offsite")
    var vectors = List[List[Float32]]()
    vectors.append(vec3(0, 1, 0))
    vectors.append(vec3(0.8, 0, 0.6))
    seed_cache(cache, LABEL, texts, vectors)
    var cfg = EmbedConfig(DEAD_URL, "test-model", LABEL, cache, INSTRUCTION)
    var left_out = embed_missing(store, cfg)
    assert_equal(left_out, 1)
    var still = store.missing_vectors(LABEL)
    assert_equal(len(still), 1)
    assert_equal(still[0].text, "unrelated grocery list")
    assert_equal(load_vectors(store, LABEL, False).count(), 2)


def test_query_lookup_order_main_then_fallback_then_miss() raises:
    """A question vector: main label in the cache, else the fallback label in the
    cache, else a call to the fallback URL (closed here, so a miss raises)."""
    set_py_path()
    var dir = temp_dir()
    var cache = dir + "/queries.sqlite"
    var question_main = "Instruct: " + INSTRUCTION + "\nQuery: question on main"
    var question_fallback = (
        "Instruct: " + INSTRUCTION + "\nQuery: question on fallback"
    )
    var question_miss = "Instruct: " + INSTRUCTION + "\nQuery: question nowhere"
    var texts_main = List[String]()
    texts_main.append(question_main)
    var vec_main = List[List[Float32]]()
    vec_main.append(vec3(1, 0, 0))
    seed_cache(cache, LABEL, texts_main, vec_main)
    var texts_fallback = List[String]()
    texts_fallback.append(question_fallback)
    var vec_fallback = List[List[Float32]]()
    vec_fallback.append(vec3(0, 0, 1))
    seed_cache(cache, "fallback-label", texts_fallback, vec_fallback)

    var cfg = EmbedConfig(DEAD_URL, "test-model", LABEL, cache, INSTRUCTION)
    cfg.set_fallback("fallback-label", DEAD_URL)

    # 1. Under the main label: that vector, and the fallback URL is never called.
    var main_hit = floats_of(embed_question(cfg, "question on main"))
    assert_equal(main_hit[0], 1.0)
    assert_equal(main_hit[2], 0.0)

    # 2. Not under the main label, but under the fallback label: that vector.
    var fallback_hit = floats_of(embed_question(cfg, "question on fallback"))
    assert_equal(fallback_hit[2], 1.0)

    # 3. In neither: the closed fallback URL is called and refuses, so the call raises.
    var raised = False
    try:
        _ = embed_question(cfg, "question nowhere")
    except:
        raised = True
    assert_true(raised)


def test_loaded_rows_are_the_stored_vectors() raises:
    """Every loaded row equals its stored vector, normalised, at the production width of
    2560 dimensions, on two loads in a row. This guards the copy in load_vectors. It does
    not reproduce the SIGSEGV of M4d: it passed on the broken code too, so the gate for that
    crash is the chat_eval repro (the crashing qids, 0 errors).
    """
    set_py_path()
    var array = Python.import_module("array")
    var dims = 2560
    var rows = 463
    var store = IndexStore(temp_dir() + "/vectors.sqlite")
    var expected = List[List[Float32]]()
    store.begin()
    for r in range(rows):
        _ = store.put(
            fact(
                "v" + String(r),
                "h" + String(r),
                "vector " + String(r),
                "active",
            )
        )
        var raw = List[Float32]()
        var py_values = Python.list()
        for j in range(dims):
            var x = Float32(((r * 31 + j * 17) % 97) - 48) / 50
            raw.append(x)
            py_values.append(PythonObject(Float64(x)))
        var blob = array.array(PythonObject("f"), py_values).tobytes()
        store.put_vector(r + 1, LABEL, blob, dims)
        normalize(raw)
        expected.append(raw^)
    store.commit()
    store.conn.commit()
    # Two loads in a row, as the chat's second recall does; each must match.
    for _ in range(2):
        var index = load_vectors(store, LABEL, False)
        assert_equal(index.count(), rows)
        var bad = 0
        for r in range(rows):
            for j in range(dims):
                var diff = index.matrix.data[r * dims + j] - expected[r][j]
                if diff < 0:
                    diff = -diff
                if diff > 1e-5:
                    bad += 1
        assert_equal(bad, 0)


def main() raises:
    test_embed_skips_facts_that_already_have_a_vector()
    print("test_embed_skips_facts_that_already_have_a_vector: ok")
    test_superseded_but_active_versions_still_need_vectors()
    print("test_superseded_but_active_versions_still_need_vectors: ok")
    test_embed_writes_vectors_from_the_cache_and_asks_no_server()
    print("test_embed_writes_vectors_from_the_cache_and_asks_no_server: ok")
    test_recall_fuses_the_vector_and_lexical_routes()
    print("test_recall_fuses_the_vector_and_lexical_routes: ok")
    test_vector_route_without_vectors_uses_no_embedding()
    print("test_vector_route_without_vectors_uses_no_embedding: ok")
    test_embed_leaves_an_unembeddable_fact_out_and_counts_it()
    print("test_embed_leaves_an_unembeddable_fact_out_and_counts_it: ok")
    test_query_lookup_order_main_then_fallback_then_miss()
    print("test_query_lookup_order_main_then_fallback_then_miss: ok")
    test_loaded_rows_are_the_stored_vectors()
    print("test_loaded_rows_are_the_stored_vectors: ok")
