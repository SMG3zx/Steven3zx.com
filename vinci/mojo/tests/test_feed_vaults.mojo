# The vaults feed on fixtures (M5 step a): sections, ids, the cursor, versions, retraction, and
# the exclusions (cold-storage, node_modules, hidden folders, the size cap). Each test writes
# its own folders in a temporary directory, so no real vault is read.

from std.python import Python, PythonObject
from std.testing import assert_equal, assert_false, assert_true

from feed import apply_read
from feed_store import FeedRead
from feed_vaults import read_vaults, sections_of
from index_store import IndexStore


def temp_dir() raises -> String:
    var tempfile = Python.import_module("tempfile")
    return String(tempfile.mkdtemp())


def write_file(path: String, text: String) raises:
    var os = Python.import_module("os")
    _ = os.makedirs(os.path.dirname(PythonObject(path)), 511, True)
    var builtins = Python.import_module("builtins")
    var f = builtins.open(PythonObject(path), PythonObject("w"))
    _ = f.write(PythonObject(text))
    f.close()


def remove_file(path: String) raises:
    var os = Python.import_module("os")
    _ = os.remove(PythonObject(path))


def rename_file(src: String, dst: String) raises:
    var os = Python.import_module("os")
    _ = os.rename(PythonObject(src), PythonObject(dst))


def plan_text() -> String:
    return (
        "# Plan\n\nIntro text.\n\n## Steps\n\nFirst step.\n\n```\n# not a"
        " heading\n```\n\n## Steps\n\nSecond steps list.\n\n###"
        " Detail\n\nDetail text.\n"
    )


def deep_text() -> String:
    return "Preamble words here.\n\n## Alpha\n\nalpha body\n"


def make_vault(root: String) raises:
    write_file(root + "/vault/plan.md", plan_text())
    write_file(root + "/vault/notes/deep.md", deep_text())
    write_file(
        root + "/vault/cold-storage/secret.md", "# Secret\n\ncold text\n"
    )
    write_file(
        root + "/vault/node_modules/pkg/readme.md", "# Readme\n\npackage text\n"
    )
    write_file(root + "/vault/.hidden/x.md", "# Hidden\n\nhidden text\n")
    var filler = String()
    for _ in range(400):
        filler += "x"
    write_file(root + "/vault/big.md", "# Big\n\n" + filler + "\n")


def line_count(text: String) raises -> Int:
    var n = 0
    for raw in text.split("\n"):
        if String(raw).byte_length() > 0:
            n += 1
    return n


def roots_of(root: String) -> List[String]:
    var out = List[String]()
    out.append(root + "/vault")
    return out^


def no_exclude() -> List[String]:
    return List[String]()


def active_count(store: IndexStore) raises -> Int:
    var row = store.conn.execute(
        "SELECT COUNT(*) FROM facts WHERE source = 'vaults' AND valid_until IS"
        " NULL AND status = 'active'"
    ).fetchone()
    return Int(String(row[0]))


def cite_of(store: IndexStore, id: String) raises -> String:
    var cite = store.latest_cite("vaults", id)
    if cite is None:
        return ""
    return cite.value().copy()


def status_of(store: IndexStore, id: String) raises -> String:
    var cur = store.current_fact("vaults", id)
    if cur is None:
        return "none"
    return cur.value().status.copy()


def test_sections_of_one_file() raises:
    var out = sections_of(
        "vault/plan.md",
        "/x/vault/plan.md",
        "2026-11-18T10:00:00+00:00",
        plan_text(),
    )
    var ids = List[String]()
    for r in out:
        ids.append(r.id.copy())
    assert_equal(len(out), 4)
    assert_equal(ids[0], "vault/plan.md#plan")
    assert_equal(ids[1], "vault/plan.md#steps")
    assert_equal(ids[2], "vault/plan.md#steps-2")
    assert_equal(ids[3], "vault/plan.md#detail")
    # The fenced "# not a heading" starts no section, and the title is the first H1.
    assert_equal(out[1].title, "Plan > Steps")
    assert_equal(out[3].title, "Plan > Steps > Detail")
    assert_equal(out[3].links[0], "vault/plan.md#steps-2")
    assert_equal(out[1].links[0], "vault/plan.md#plan")
    assert_equal(out[0].kind, "section")
    assert_false(out[0].trusted)
    assert_true(out[0].text.startswith("Plan\n\n# Plan"))


def test_preamble_and_file_name_title() raises:
    var out = sections_of(
        "vault/notes/deep.md",
        "/x/deep.md",
        "2026-11-18T10:00:00+00:00",
        deep_text(),
    )
    assert_equal(len(out), 2)
    assert_equal(out[0].id, "vault/notes/deep.md#top")
    assert_equal(out[0].title, "deep")
    assert_equal(out[1].id, "vault/notes/deep.md#alpha")
    assert_equal(out[1].title, "deep > Alpha")
    assert_equal(len(out[1].links), 0)


def test_walk_exclusions_and_cursor() raises:
    var dir = temp_dir()
    make_vault(dir)
    var first = read_vaults(roots_of(dir), no_exclude(), 300, String())
    # plan.md has 4 sections and deep.md has 2. big.md is over the cap, and cold-storage,
    # node_modules and the hidden folder are never entered.
    assert_equal(len(first.records), 6)
    assert_equal(len(first.all_ids), 6)
    assert_true(first.complete)
    for r in first.records:
        assert_false(r.id.find("cold") >= 0, "cold-storage must never be read")
        assert_false(
            r.id.find("readme") >= 0, "node_modules must never be read"
        )
        assert_false(
            r.id.find("hidden") >= 0, "hidden folders must never be read"
        )
        assert_false(
            r.id.find("big.md") >= 0, "files over the size cap are skipped"
        )
    # The cursor has one line per file that was read (plan.md and deep.md).
    assert_equal(line_count(first.cursor), 2)


def test_second_read_changes_nothing() raises:
    var dir = temp_dir()
    make_vault(dir)
    var store = IndexStore(dir + "/index.sqlite")
    var first = read_vaults(roots_of(dir), no_exclude(), 300, String())
    var report = apply_read(
        store, "vaults", first, "2026-11-18T10:00:00+00:00", 0.0
    )
    assert_equal(report.new, 6)
    assert_equal(report.versions, 0)
    assert_equal(report.retracted, 0)
    assert_equal(active_count(store), 6)

    var second = read_vaults(roots_of(dir), no_exclude(), 300, first.cursor)
    assert_equal(len(second.records), 0)
    var again = apply_read(
        store, "vaults", second, "2026-11-18T10:05:00+00:00", 0.0
    )
    assert_equal(again.new, 0)
    assert_equal(again.versions, 0)
    assert_equal(again.retracted, 0)
    assert_equal(again.unchanged, 0)
    assert_equal(active_count(store), 6)


def test_edit_rename_delete_and_restore() raises:
    var dir = temp_dir()
    make_vault(dir)
    var store = IndexStore(dir + "/index.sqlite")
    var first = read_vaults(roots_of(dir), no_exclude(), 300, String())
    _ = apply_read(store, "vaults", first, "2026-11-18T10:00:00+00:00", 0.0)
    var steps_cite = cite_of(store, "vault/plan.md#steps")
    assert_true(steps_cite.startswith("v"), "a section gets a v citation id")

    # An edit to one section is a new version of that section, and nothing else moves.
    write_file(
        dir + "/vault/plan.md",
        plan_text().replace("First step.", "First step, edited."),
    )
    var edited = read_vaults(roots_of(dir), no_exclude(), 300, first.cursor)
    assert_equal(len(edited.records), 4)
    var after_edit = apply_read(
        store, "vaults", edited, "2026-11-18T10:10:00+00:00", 0.0
    )
    assert_equal(after_edit.new, 0)
    assert_equal(after_edit.versions, 1)
    assert_equal(after_edit.retracted, 0)
    assert_equal(cite_of(store, "vault/plan.md#steps"), steps_cite)

    # A renamed file is new records under the new path, and the old path's records retract.
    rename_file(dir + "/vault/notes/deep.md", dir + "/vault/notes/renamed.md")
    var renamed = read_vaults(roots_of(dir), no_exclude(), 300, edited.cursor)
    var after_rename = apply_read(
        store, "vaults", renamed, "2026-11-18T10:20:00+00:00", 0.0
    )
    assert_equal(after_rename.new, 2)
    assert_equal(after_rename.retracted, 2)
    assert_equal(status_of(store, "vault/notes/deep.md#top"), "retracted")
    assert_equal(status_of(store, "vault/notes/renamed.md#top"), "active")

    # A deleted file retracts all its sections, and a retracted section is not active.
    remove_file(dir + "/vault/plan.md")
    var deleted = read_vaults(roots_of(dir), no_exclude(), 300, renamed.cursor)
    var after_delete = apply_read(
        store, "vaults", deleted, "2026-11-18T10:30:00+00:00", 0.0
    )
    assert_equal(after_delete.new, 0)
    assert_equal(after_delete.retracted, 4)
    assert_equal(status_of(store, "vault/plan.md#steps"), "retracted")
    assert_equal(active_count(store), 2)

    # Restoring the file revives its records as new versions under the same citation ids.
    write_file(dir + "/vault/plan.md", plan_text())
    var restored = read_vaults(roots_of(dir), no_exclude(), 300, deleted.cursor)
    var after_restore = apply_read(
        store, "vaults", restored, "2026-11-18T10:40:00+00:00", 0.0
    )
    assert_equal(after_restore.new, 0)
    assert_equal(after_restore.versions, 4)
    assert_equal(status_of(store, "vault/plan.md#steps"), "active")
    assert_equal(cite_of(store, "vault/plan.md#steps"), steps_cite)
    assert_equal(active_count(store), 6)


def test_a_removed_section_is_retracted() raises:
    var dir = temp_dir()
    make_vault(dir)
    var store = IndexStore(dir + "/index.sqlite")
    var first = read_vaults(roots_of(dir), no_exclude(), 300, String())
    _ = apply_read(store, "vaults", first, "2026-11-18T10:00:00+00:00", 0.0)
    write_file(
        dir + "/vault/plan.md",
        plan_text().replace("\n### Detail\n\nDetail text.\n", "\n"),
    )
    var cut = read_vaults(roots_of(dir), no_exclude(), 300, first.cursor)
    var report = apply_read(
        store, "vaults", cut, "2026-11-18T10:10:00+00:00", 0.0
    )
    assert_equal(report.retracted, 1)
    assert_equal(status_of(store, "vault/plan.md#detail"), "retracted")
    assert_equal(status_of(store, "vault/plan.md#steps"), "active")


def test_unread_cold_storage_root_is_refused() raises:
    var dir = temp_dir()
    make_vault(dir)
    var refused = False
    try:
        var cold = List[String]()
        cold.append(dir + "/vault/cold-storage")
        _ = read_vaults(cold, no_exclude(), 300, String())
    except:
        refused = True
    assert_true(refused, "a root inside cold-storage must be refused")


def main() raises:
    test_sections_of_one_file()
    print("test_sections_of_one_file: ok")
    test_preamble_and_file_name_title()
    print("test_preamble_and_file_name_title: ok")
    test_walk_exclusions_and_cursor()
    print("test_walk_exclusions_and_cursor: ok")
    test_second_read_changes_nothing()
    print("test_second_read_changes_nothing: ok")
    test_edit_rename_delete_and_restore()
    print("test_edit_rename_delete_and_restore: ok")
    test_a_removed_section_is_retracted()
    print("test_a_removed_section_is_retracted: ok")
    test_unread_cold_storage_root_is_refused()
    print("test_unread_cold_storage_root_is_refused: ok")
