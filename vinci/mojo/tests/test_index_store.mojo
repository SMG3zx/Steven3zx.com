# Index store tests: the versioning rules of IndexStore.put, on in-memory databases.
# A version is current when valid_until is NULL. The same latest hash is a no-op,
# a new hash supersedes, and an older hash comes back as a new version (A, B, A
# leaves A current). Older versions are never compared against.

from std.python import Python
from std.testing import assert_equal, assert_false, assert_true

from index_store import Fact, IndexStore


def fact(
    record_id: String, version_hash: String, text: String, at: String
) raises -> Fact:
    var tags = List[String]()
    var refs = List[String]()
    return Fact(
        "test",
        record_id,
        version_hash,
        "idea",
        text,
        record_id,
        at,
        None,
        tags^,
        refs^,
        True,
        "active",
        1,
    )


def current_hash(store: IndexStore, record_id: String) raises -> String:
    var cur = store.current("test", record_id)
    assert_true(cur is not None, "expected a current version of " + record_id)
    return cur.value().copy().version_hash


def test_same_latest_hash_is_a_no_op() raises:
    var store = IndexStore(":memory:")
    assert_equal(
        store.put(fact("r1", "hA", "alpha", "2026-01-01T00:00:00Z")), 1
    )
    assert_equal(
        store.put(fact("r1", "hA", "alpha", "2026-02-01T00:00:00Z")), 0
    )
    assert_equal(store.fact_total(), 1)


def test_new_hash_supersedes_the_old_version() raises:
    var store = IndexStore(":memory:")
    _ = store.put(fact("r1", "hA", "alpha", "2026-01-01T00:00:00Z"))
    assert_equal(store.put(fact("r1", "hB", "beta", "2026-02-01T00:00:00Z")), 2)
    assert_equal(store.fact_total(), 2)
    assert_equal(current_hash(store, "r1"), "hB")
    # The old version ends at the time of the line that replaced it.
    var old = store.conn.execute(
        "SELECT valid_until FROM facts WHERE version_hash = 'hA'"
    ).fetchone()
    assert_equal(String(old[0]), "2026-02-01T00:00:00Z")


def test_going_back_to_an_older_hash_is_a_new_version() raises:
    var store = IndexStore(":memory:")
    _ = store.put(fact("r1", "hA", "alpha", "2026-01-01T00:00:00Z"))
    _ = store.put(fact("r1", "hB", "beta", "2026-02-01T00:00:00Z"))
    assert_equal(
        store.put(fact("r1", "hA", "alpha", "2026-03-01T00:00:00Z")), 2
    )
    assert_equal(store.fact_total(), 3)
    assert_equal(current_hash(store, "r1"), "hA")
    # Exactly one current row per record.
    var current_rows = store.conn.execute(
        "SELECT COUNT(*) FROM facts WHERE record_id = 'r1'"
        " AND valid_until IS NULL"
    ).fetchone()
    assert_equal(String(current_rows[0]), "1")


def test_records_are_keyed_by_source_and_id() raises:
    var store = IndexStore(":memory:")
    _ = store.put(fact("r1", "hA", "alpha", "2026-01-01T00:00:00Z"))
    var other = fact("r1", "hA", "alpha", "2026-01-01T00:00:00Z")
    other.source = "elsewhere"
    assert_equal(store.put(other), 1)
    assert_equal(store.fact_total(), 2)


def test_end_version_leaves_no_current_version() raises:
    var store = IndexStore(":memory:")
    _ = store.put(fact("r1", "hA", "alpha", "2026-01-01T00:00:00Z"))
    assert_equal(store.end_version("test", "r1", "2026-05-01T00:00:00Z"), 1)
    assert_true(store.current("test", "r1") is None)
    assert_true(store.has_key("test", "r1"))
    assert_equal(store.end_version("test", "r1", "2026-06-01T00:00:00Z"), 0)


def test_schema_version_is_checked_on_open() raises:
    # A file, so that the second open sees what the first one stored.
    var tempfile = Python.import_module("tempfile")
    var path = String(tempfile.mkdtemp()) + "/index.sqlite"
    var store = IndexStore(path)
    assert_equal(store.meta_get("schema_version"), "1")
    store.meta_set("schema_version", "99")
    store.conn.commit()
    var refused = False
    try:
        _ = IndexStore(path)
    except:
        refused = True
    assert_true(refused, "an index with another schema version must be refused")


def main() raises:
    test_same_latest_hash_is_a_no_op()
    print("test_same_latest_hash_is_a_no_op: ok")
    test_new_hash_supersedes_the_old_version()
    print("test_new_hash_supersedes_the_old_version: ok")
    test_going_back_to_an_older_hash_is_a_new_version()
    print("test_going_back_to_an_older_hash_is_a_new_version: ok")
    test_records_are_keyed_by_source_and_id()
    print("test_records_are_keyed_by_source_and_id: ok")
    test_end_version_leaves_no_current_version()
    print("test_end_version_leaves_no_current_version: ok")
    test_schema_version_is_checked_on_open()
    print("test_schema_version_is_checked_on_open: ok")
