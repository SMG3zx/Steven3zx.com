# The git feed on a repository made in a temporary directory (M5 step a): the commits, the
# cursor, an incremental read, the full scan after a rewrite, and the retraction that follows.
# The real monorepo is never read by a test.

from std.python import Python, PythonObject
from std.testing import assert_equal, assert_false, assert_true

from feed import apply_read
from feed_git import read_git
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


def git(repo: String, args: List[String]) raises -> String:
    """Run git in repo, with the test's identity, and return its output. Raises on failure.
    """
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
    return String(String(done.stdout).strip())


def git_args(args: String) -> List[String]:
    """The words of one git command line, split on single spaces."""
    var out = List[String]()
    for word in args.split(" "):
        out.append(String(word))
    return out^


def commit(repo: String, message: String, body: String) raises:
    var args = git_args("commit -q --allow-empty-message")
    args.append("-m")
    args.append(message)
    if body.byte_length() > 0:
        args.append("-m")
        args.append(body)
    _ = git(repo, args)


def count_status(store: IndexStore, status: String) raises -> Int:
    var row = store.conn.execute(
        (
            "SELECT COUNT(*) FROM facts WHERE source = 'git' AND status = ?"
            " AND valid_until IS NULL"
        ),
        py_params(PythonObject(status)),
    ).fetchone()
    return Int(String(row[0]))


def fresh_repo() raises -> String:
    """A repository with three commits on master: add alpha, add beta and edit alpha, rename.
    """
    var repo = temp_dir() + "/repo"
    var os = Python.import_module("os")
    _ = os.makedirs(PythonObject(repo), 511, True)
    _ = git(repo, git_args("init -q -b master"))
    write_file(repo + "/alpha/one.md", "one\n")
    _ = git(repo, git_args("add -A"))
    commit(repo, "Add alpha", "")

    write_file(repo + "/beta/two.md", "two\n")
    write_file(repo + "/alpha/one.md", "one\nextra\n")
    _ = git(repo, git_args("add -A"))
    commit(repo, "Add beta and edit alpha", "Body line.")

    _ = git(repo, git_args("mv alpha/one.md alpha/renamed.md"))
    commit(repo, "Rename one", "")
    return repo


def test_full_read_then_nothing_new() raises:
    var repo = fresh_repo()
    var store = IndexStore(temp_dir() + "/index.sqlite")
    var first = read_git(repo, "master", String())
    assert_equal(len(first.records), 3)
    assert_true(first.complete)
    assert_equal(len(first.all_ids), 3)
    assert_true(first.cursor.startswith("master:"))
    var report = apply_read(
        store, "git", first, "2026-11-18T10:00:00+00:00", 0.0
    )
    assert_equal(report.new, 3)
    assert_equal(report.retracted, 0)

    var again = read_git(repo, "master", first.cursor)
    assert_equal(len(again.records), 0)
    var quiet = apply_read(
        store, "git", again, "2026-11-18T10:01:00+00:00", 0.0
    )
    assert_equal(quiet.new, 0)
    assert_equal(quiet.versions, 0)
    assert_equal(quiet.retracted, 0)


def test_commit_record_text_and_links() raises:
    var repo = fresh_repo()
    var first = read_git(repo, "master", String())
    var middle = first.records[1].copy()
    assert_equal(middle.kind, "commit")
    assert_equal(middle.title, "Add beta and edit alpha")
    assert_false(middle.trusted)
    assert_equal(middle.id.byte_length(), 10)
    assert_true(middle.text.find("Body line.") >= 0)
    assert_true(middle.text.find("commit " + middle.id) >= 0)
    assert_true(middle.text.find("Changed 2 files, +2 -0") >= 0)
    assert_true(middle.text.find("  beta/two.md  +1 -0") >= 0)
    # Links: the parent's id, then one project per top-level folder touched.
    var has_parent = False
    var has_alpha = False
    var has_beta = False
    for link in middle.links:
        if link.startswith("project:alpha"):
            has_alpha = True
        if link.startswith("project:beta"):
            has_beta = True
        if link.byte_length() == 10:
            has_parent = True
    assert_true(has_alpha)
    assert_true(has_beta)
    assert_true(has_parent)


def test_incremental_then_rewrite() raises:
    var repo = fresh_repo()
    var store = IndexStore(temp_dir() + "/index.sqlite")
    var first = read_git(repo, "master", String())
    _ = apply_read(store, "git", first, "2026-11-18T10:00:00+00:00", 0.0)

    # A new commit is read alone: the cursor covers the rest.
    write_file(repo + "/gamma/three.md", "three\n")
    _ = git(repo, git_args("add -A"))
    commit(repo, "Add gamma", "")
    var grown = read_git(repo, "master", first.cursor)
    assert_equal(len(grown.records), 1)
    assert_false(grown.complete)
    assert_true(grown.note.find("since") >= 0)
    var added = apply_read(
        store, "git", grown, "2026-11-18T10:05:00+00:00", 0.0
    )
    assert_equal(added.new, 1)
    assert_equal(added.retracted, 0)

    # Amending the tip rewrites it: the cursor is no longer an ancestor, so the branch is read
    # in full. The old tip is retracted, the new one is new, and the rest are unchanged.
    _ = git(repo, git_args("commit -q --amend --no-edit -m Add-gamma-again"))
    var rewritten = read_git(repo, "master", grown.cursor)
    assert_true(rewritten.complete)
    assert_equal(len(rewritten.records), 4)
    assert_true(rewritten.note.find("rewritten") >= 0)
    var redo = apply_read(
        store, "git", rewritten, "2026-11-18T10:10:00+00:00", 0.0
    )
    assert_equal(redo.new, 1)
    assert_equal(redo.retracted, 1)
    assert_equal(count_status(store, "retracted"), 1)
    assert_equal(count_status(store, "active"), 4)


def test_a_missing_branch_is_an_error() raises:
    var repo = fresh_repo()
    var refused = False
    try:
        _ = read_git(repo, "no-such-branch", String())
    except:
        refused = True
    assert_true(refused)


def main() raises:
    test_full_read_then_nothing_new()
    print("test_full_read_then_nothing_new: ok")
    test_commit_record_text_and_links()
    print("test_commit_record_text_and_links: ok")
    test_incremental_then_rewrite()
    print("test_incremental_then_rewrite: ok")
    test_a_missing_branch_is_an_error()
    print("test_a_missing_branch_is_an_error: ok")
