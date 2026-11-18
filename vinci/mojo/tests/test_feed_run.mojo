# The feeds as a whole (M5 step a): the journal's rewrite check, the config, a reset, and the
# done criterion: with every feed run once, a second run adds nothing (0 new records, 0 new
# versions). Each test works in a temporary directory, never in the real VINCI_HOME.

from std.python import Python, PythonObject
from std.testing import assert_equal, assert_false, assert_true

from feed import FeedConfig, load_config, reset_feed, run_feed
from index_build import update_from_journal
from index_store import IndexStore, py_params


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


def git(repo: String, args: List[String]) raises:
    var subprocess = Python.import_module("subprocess")
    var cmd = Python.list()
    cmd.append(PythonObject("git"))
    cmd.append(PythonObject("-c"))
    cmd.append(PythonObject("user.name=Test"))
    cmd.append(PythonObject("-c"))
    cmd.append(PythonObject("user.email=test@example.com"))
    cmd.append(PythonObject("-C"))
    cmd.append(PythonObject(repo))
    for a in args:
        cmd.append(PythonObject(a))
    var done = subprocess.run(cmd, capture_output=True, text=True, check=False)
    if Int(String(done.returncode)) != 0:
        raise Error("test git failed: " + String(done.stderr))


def journal_line(text: String) -> String:
    return (
        '{"at":"2026-11-10T09:00:00-06:00","kind":"idea","id":"i1","text":"'
        + text
        + '"}\n'
    )


def current_text_count(store: IndexStore, text: String) raises -> Int:
    var row = store.conn.execute(
        (
            "SELECT COUNT(*) FROM facts WHERE source = 'journal' AND text = ?"
            " AND valid_until IS NULL"
        ),
        py_params(PythonObject(text)),
    ).fetchone()
    return Int(String(row[0]))


def test_a_rewritten_journal_is_read_again() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    write_file(journal, journal_line("good"))
    var store = IndexStore(dir + "/index.sqlite")
    var first = update_from_journal(store, journal, dir)
    assert_false(first.rebuilt)
    assert_equal(current_text_count(store, "good"), 1)

    # Same length, different first line: the prefix hash changes, so the facts are read again.
    write_file(journal, journal_line("evil"))
    var rewritten = update_from_journal(store, journal, dir)
    assert_true(rewritten.rebuilt)
    assert_equal(current_text_count(store, "good"), 0)
    assert_equal(current_text_count(store, "evil"), 1)

    # An append is not a rewrite: the prefix still matches, so no rebuild.
    write_file(journal, journal_line("evil") + journal_line("more"))
    var appended = update_from_journal(store, journal, dir)
    assert_false(appended.rebuilt)
    assert_equal(current_text_count(store, "more"), 1)


def test_config_defaults_and_errors() raises:
    var dir = temp_dir()
    var defaults = load_config(dir + "/feeds.toml")
    assert_true(defaults.git_enabled)
    assert_true(defaults.vault_enabled)
    assert_equal(defaults.git_branch, "master")
    assert_equal(len(defaults.vault_roots), 3)

    write_file(dir + "/feeds.toml", '[git]\npath = "/somewhere"\nbogus = 1\n')
    var refused = False
    try:
        _ = load_config(dir + "/feeds.toml")
    except:
        refused = True
    assert_true(refused, "an unknown key must stop the command")


def test_reset_then_read_again() raises:
    var dir = temp_dir()
    var journal = dir + "/journal.jsonl"
    write_file(journal, journal_line("good") + journal_line("second"))
    var store = IndexStore(dir + "/index.sqlite")
    var cfg = FeedConfig()
    _ = run_feed(
        store, "journal", cfg, journal, dir, "2026-11-18T10:00:00+00:00"
    )
    var before = store.fact_total()
    reset_feed(store, "journal")
    assert_equal(store.fact_total(), 0)
    var report = run_feed(
        store, "journal", cfg, journal, dir, "2026-11-18T10:01:00+00:00"
    )
    assert_equal(report.new, 2)
    assert_equal(store.fact_total(), before)


def test_every_feed_adds_nothing_on_the_second_run() raises:
    var dir = temp_dir()
    # A journal, a git repository and a vault, all in the temporary directory.
    var journal = dir + "/journal.jsonl"
    write_file(journal, journal_line("good") + journal_line("second"))
    var repo = dir + "/repo"
    write_file(repo + "/src/a.mojo", "alpha\n")
    git(repo, ["init", "-q", "-b", "master"])
    git(repo, ["add", "-A"])
    git(repo, ["commit", "-q", "-m", "First"])
    write_file(repo + "/docs/b.md", "beta\n")
    git(repo, ["add", "-A"])
    git(repo, ["commit", "-q", "-m", "Second", "-m", "With a body."])
    write_file(
        dir + "/vault/notes/plan.md",
        "# Plan\n\nText here.\n\n## Next\n\nMore.\n",
    )
    write_file(dir + "/vault/cold-storage/old.md", "# Old\n\ncold\n")

    write_file(
        dir + "/feeds.toml",
        '[git]\npath = "'
        + repo
        + '"\nbranch = "master"\n\n[vaults]\nroots = ["'
        + dir
        + '/vault"]\n',
    )
    var cfg = load_config(dir + "/feeds.toml")
    var store = IndexStore(dir + "/index.sqlite")
    var now = "2026-11-18T10:00:00+00:00"

    var names = ["journal", "git", "vaults"]
    var first_new = 0
    for name in names:
        var report = run_feed(store, name, cfg, journal, dir, now)
        first_new += report.new
    assert_equal(first_new, 2 + 2 + 2)

    # The done criterion: a second run of every feed adds nothing.
    for name in names:
        var again = run_feed(store, name, cfg, journal, dir, now)
        assert_equal(again.new, 0, name + " added records on the second run")
        assert_equal(
            again.versions, 0, name + " added versions on the second run"
        )
        assert_equal(again.retracted, 0, name + " retracted on the second run")


def main() raises:
    test_a_rewritten_journal_is_read_again()
    print("test_a_rewritten_journal_is_read_again: ok")
    test_config_defaults_and_errors()
    print("test_config_defaults_and_errors: ok")
    test_reset_then_read_again()
    print("test_reset_then_read_again: ok")
    test_every_feed_adds_nothing_on_the_second_run()
    print("test_every_feed_adds_nothing_on_the_second_run: ok")
