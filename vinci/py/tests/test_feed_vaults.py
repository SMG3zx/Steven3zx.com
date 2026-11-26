"""The vaults feed on fixtures (M5 step a): sections, ids, the cursor, versions, retraction, and the
exclusions (cold-storage, node_modules, hidden folders, the size cap). Each test writes its own
folders in a temporary directory, so no real vault is read."""

from pathlib import Path

import pytest

from vinci_core.feed import apply_read
from vinci_core.feed_vaults import read_vaults, sections_of
from vinci_core.index_store import IndexStore


def write_file(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def plan_text() -> str:
    return (
        "# Plan\n\nIntro text.\n\n## Steps\n\nFirst step.\n\n```\n# not a heading\n```\n\n"
        "## Steps\n\nSecond steps list.\n\n### Detail\n\nDetail text.\n"
    )


def deep_text() -> str:
    return "Preamble words here.\n\n## Alpha\n\nalpha body\n"


def make_vault(root: Path) -> None:
    write_file(root / "vault" / "plan.md", plan_text())
    write_file(root / "vault" / "notes" / "deep.md", deep_text())
    write_file(root / "vault" / "cold-storage" / "secret.md", "# Secret\n\ncold text\n")
    write_file(root / "vault" / "node_modules" / "pkg" / "readme.md", "# Readme\n\npackage text\n")
    write_file(root / "vault" / ".hidden" / "x.md", "# Hidden\n\nhidden text\n")
    write_file(root / "vault" / "big.md", "# Big\n\n" + "x" * 400 + "\n")


def line_count(text: str) -> int:
    return sum(1 for raw in text.split("\n") if raw != "")


def roots_of(root: Path) -> list[str]:
    return [str(root / "vault")]


def active_count(store: IndexStore) -> int:
    row = store.conn.execute(
        "SELECT COUNT(*) FROM facts WHERE source = 'vaults' AND valid_until IS NULL AND status = 'active'"
    ).fetchone()
    return int(row[0])


def cite_of(store: IndexStore, ident: str) -> str:
    cite = store.latest_cite("vaults", ident)
    return "" if cite is None else cite


def status_of(store: IndexStore, ident: str) -> str:
    cur = store.current_fact("vaults", ident)
    return "none" if cur is None else cur.status


def test_sections_of_one_file() -> None:
    out = sections_of("vault/plan.md", "/x/vault/plan.md", "2026-11-18T10:00:00+00:00", plan_text())
    ids = [r.id for r in out]
    assert len(out) == 4
    assert ids[0] == "vault/plan.md#plan"
    assert ids[1] == "vault/plan.md#steps"
    assert ids[2] == "vault/plan.md#steps-2"
    assert ids[3] == "vault/plan.md#detail"
    # The fenced "# not a heading" starts no section, and the title is the first H1.
    assert out[1].title == "Plan > Steps"
    assert out[3].title == "Plan > Steps > Detail"
    assert out[3].links[0] == "vault/plan.md#steps-2"
    assert out[1].links[0] == "vault/plan.md#plan"
    assert out[0].kind == "section"
    assert out[0].trusted is False
    assert out[0].text.startswith("Plan\n\n# Plan")


def test_preamble_and_file_name_title() -> None:
    out = sections_of("vault/notes/deep.md", "/x/deep.md", "2026-11-18T10:00:00+00:00", deep_text())
    assert len(out) == 2
    assert out[0].id == "vault/notes/deep.md#top"
    assert out[0].title == "deep"
    assert out[1].id == "vault/notes/deep.md#alpha"
    assert out[1].title == "deep > Alpha"
    assert len(out[1].links) == 0


def test_walk_exclusions_and_cursor(tmp_path: Path) -> None:
    make_vault(tmp_path)
    first = read_vaults(roots_of(tmp_path), [], 300, "")
    # plan.md has 4 sections and deep.md has 2. big.md is over the cap, and cold-storage,
    # node_modules and the hidden folder are never entered.
    assert len(first.records) == 6
    assert len(first.all_ids) == 6
    assert first.complete
    for r in first.records:
        assert "cold" not in r.id, "cold-storage must never be read"
        assert "readme" not in r.id, "node_modules must never be read"
        assert "hidden" not in r.id, "hidden folders must never be read"
        assert "big.md" not in r.id, "files over the size cap are skipped"
    # The cursor has one line per file that was read (plan.md and deep.md).
    assert line_count(first.cursor) == 2


def test_second_read_changes_nothing(tmp_path: Path) -> None:
    make_vault(tmp_path)
    store = IndexStore(str(tmp_path / "index.sqlite"))
    first = read_vaults(roots_of(tmp_path), [], 300, "")
    report = apply_read(store, "vaults", first, "2026-11-18T10:00:00+00:00", 0.0)
    assert report.new == 6
    assert report.versions == 0
    assert report.retracted == 0
    assert active_count(store) == 6

    second = read_vaults(roots_of(tmp_path), [], 300, first.cursor)
    assert len(second.records) == 0
    again = apply_read(store, "vaults", second, "2026-11-18T10:05:00+00:00", 0.0)
    assert again.new == 0
    assert again.versions == 0
    assert again.retracted == 0
    assert again.unchanged == 0
    assert active_count(store) == 6


def test_edit_rename_delete_and_restore(tmp_path: Path) -> None:
    make_vault(tmp_path)
    store = IndexStore(str(tmp_path / "index.sqlite"))
    first = read_vaults(roots_of(tmp_path), [], 300, "")
    apply_read(store, "vaults", first, "2026-11-18T10:00:00+00:00", 0.0)
    steps_cite = cite_of(store, "vault/plan.md#steps")
    assert steps_cite.startswith("v"), "a section gets a v citation id"

    # An edit to one section is a new version of that section, and nothing else moves.
    write_file(tmp_path / "vault" / "plan.md", plan_text().replace("First step.", "First step, edited."))
    edited = read_vaults(roots_of(tmp_path), [], 300, first.cursor)
    assert len(edited.records) == 4
    after_edit = apply_read(store, "vaults", edited, "2026-11-18T10:10:00+00:00", 0.0)
    assert after_edit.new == 0
    assert after_edit.versions == 1
    assert after_edit.retracted == 0
    assert cite_of(store, "vault/plan.md#steps") == steps_cite

    # A renamed file is new records under the new path, and the old path's records retract.
    (tmp_path / "vault" / "notes" / "deep.md").rename(tmp_path / "vault" / "notes" / "renamed.md")
    renamed = read_vaults(roots_of(tmp_path), [], 300, edited.cursor)
    after_rename = apply_read(store, "vaults", renamed, "2026-11-18T10:20:00+00:00", 0.0)
    assert after_rename.new == 2
    assert after_rename.retracted == 2
    assert status_of(store, "vault/notes/deep.md#top") == "retracted"
    assert status_of(store, "vault/notes/renamed.md#top") == "active"

    # A deleted file retracts all its sections, and a retracted section is not active.
    (tmp_path / "vault" / "plan.md").unlink()
    deleted = read_vaults(roots_of(tmp_path), [], 300, renamed.cursor)
    after_delete = apply_read(store, "vaults", deleted, "2026-11-18T10:30:00+00:00", 0.0)
    assert after_delete.new == 0
    assert after_delete.retracted == 4
    assert status_of(store, "vault/plan.md#steps") == "retracted"
    assert active_count(store) == 2

    # Restoring the file revives its records as new versions under the same citation ids.
    write_file(tmp_path / "vault" / "plan.md", plan_text())
    restored = read_vaults(roots_of(tmp_path), [], 300, deleted.cursor)
    after_restore = apply_read(store, "vaults", restored, "2026-11-18T10:40:00+00:00", 0.0)
    assert after_restore.new == 0
    assert after_restore.versions == 4
    assert status_of(store, "vault/plan.md#steps") == "active"
    assert cite_of(store, "vault/plan.md#steps") == steps_cite
    assert active_count(store) == 6


def test_a_removed_section_is_retracted(tmp_path: Path) -> None:
    make_vault(tmp_path)
    store = IndexStore(str(tmp_path / "index.sqlite"))
    first = read_vaults(roots_of(tmp_path), [], 300, "")
    apply_read(store, "vaults", first, "2026-11-18T10:00:00+00:00", 0.0)
    write_file(tmp_path / "vault" / "plan.md", plan_text().replace("\n### Detail\n\nDetail text.\n", "\n"))
    cut = read_vaults(roots_of(tmp_path), [], 300, first.cursor)
    report = apply_read(store, "vaults", cut, "2026-11-18T10:10:00+00:00", 0.0)
    assert report.retracted == 1
    assert status_of(store, "vault/plan.md#detail") == "retracted"
    assert status_of(store, "vault/plan.md#steps") == "active"


def test_unread_cold_storage_root_is_refused(tmp_path: Path) -> None:
    make_vault(tmp_path)
    with pytest.raises(Exception):
        read_vaults([str(tmp_path / "vault" / "cold-storage")], [], 300, "")
