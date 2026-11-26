"""Index store tests: the versioning rules of IndexStore.put, on in-memory databases. A version is
current when valid_until is NULL. The same latest hash is a no-op, a new hash supersedes, and an
older hash comes back as a new version (A, B, A leaves A current)."""

import pytest

from vinci_core.index_store import Fact, IndexStore


def fact(record_id: str, version_hash: str, text: str, at: str) -> Fact:
    return Fact("test", record_id, version_hash, "idea", text, record_id, at, None, [], [], True, "active", 1)


def current_hash(store: IndexStore, record_id: str) -> str:
    cur = store.current("test", record_id)
    assert cur is not None, "expected a current version of " + record_id
    return cur.version_hash


def test_same_latest_hash_is_a_no_op() -> None:
    store = IndexStore(":memory:")
    assert store.put(fact("r1", "hA", "alpha", "2026-01-01T00:00:00Z")) == 1
    assert store.put(fact("r1", "hA", "alpha", "2026-02-01T00:00:00Z")) == 0
    assert store.fact_total() == 1


def test_new_hash_supersedes_the_old_version() -> None:
    store = IndexStore(":memory:")
    store.put(fact("r1", "hA", "alpha", "2026-01-01T00:00:00Z"))
    assert store.put(fact("r1", "hB", "beta", "2026-02-01T00:00:00Z")) == 2
    assert store.fact_total() == 2
    assert current_hash(store, "r1") == "hB"
    # The old version ends at the time of the line that replaced it.
    old = store.conn.execute("SELECT valid_until FROM facts WHERE version_hash = 'hA'").fetchone()
    assert str(old[0]) == "2026-02-01T00:00:00Z"


def test_going_back_to_an_older_hash_is_a_new_version() -> None:
    store = IndexStore(":memory:")
    store.put(fact("r1", "hA", "alpha", "2026-01-01T00:00:00Z"))
    store.put(fact("r1", "hB", "beta", "2026-02-01T00:00:00Z"))
    assert store.put(fact("r1", "hA", "alpha", "2026-03-01T00:00:00Z")) == 2
    assert store.fact_total() == 3
    assert current_hash(store, "r1") == "hA"
    # Exactly one current row per record.
    rows = store.conn.execute("SELECT COUNT(*) FROM facts WHERE record_id = 'r1' AND valid_until IS NULL").fetchone()
    assert str(rows[0]) == "1"


def test_records_are_keyed_by_source_and_id() -> None:
    store = IndexStore(":memory:")
    store.put(fact("r1", "hA", "alpha", "2026-01-01T00:00:00Z"))
    other = fact("r1", "hA", "alpha", "2026-01-01T00:00:00Z")
    other.source = "elsewhere"
    assert store.put(other) == 1
    assert store.fact_total() == 2


def test_end_version_leaves_no_current_version() -> None:
    store = IndexStore(":memory:")
    store.put(fact("r1", "hA", "alpha", "2026-01-01T00:00:00Z"))
    assert store.end_version("test", "r1", "2026-05-01T00:00:00Z") == 1
    assert store.current("test", "r1") is None
    assert store.has_key("test", "r1")
    assert store.end_version("test", "r1", "2026-06-01T00:00:00Z") == 0


def test_schema_version_is_checked_on_open(tmp_path) -> None:  # type: ignore[no-untyped-def]
    """A file, so that the second open sees what the first one stored."""
    path = str(tmp_path / "index.sqlite")
    store = IndexStore(path)
    assert store.meta_get("schema_version") == "1"
    store.meta_set("schema_version", "99")
    store.conn.commit()
    with pytest.raises(ValueError):
        IndexStore(path)
