"""The git feed on a repository made in a temporary directory (M5 step a): the commits, the cursor,
an incremental read, the full scan after a rewrite, and the retraction that follows. The real
monorepo is never read by a test."""

import subprocess
from pathlib import Path

import pytest

from vinci_core.feed import apply_read
from vinci_core.feed_git import read_git
from vinci_core.index_store import IndexStore


def write_file(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def git(repo: Path, *args: str) -> str:
    done = subprocess.run(
        ["git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "-C", str(repo), *args],
        capture_output=True,
        text=True,
        check=False,
    )
    if done.returncode != 0:
        raise AssertionError("test git failed: " + done.stderr)
    return done.stdout.strip()


def commit(repo: Path, message: str, body: str) -> None:
    args = ["commit", "-q", "--allow-empty-message", "-m", message]
    if body != "":
        args += ["-m", body]
    git(repo, *args)


def count_status(store: IndexStore, status: str) -> int:
    row = store.conn.execute(
        "SELECT COUNT(*) FROM facts WHERE source = 'git' AND status = ? AND valid_until IS NULL",
        (status,),
    ).fetchone()
    return int(row[0])


def fresh_repo(root: Path) -> Path:
    """A repository with three commits on master: add alpha, add beta and edit alpha, rename."""
    repo = root / "repo"
    repo.mkdir(parents=True)
    git(repo, "init", "-q", "-b", "master")
    write_file(repo / "alpha" / "one.md", "one\n")
    git(repo, "add", "-A")
    commit(repo, "Add alpha", "")

    write_file(repo / "beta" / "two.md", "two\n")
    write_file(repo / "alpha" / "one.md", "one\nextra\n")
    git(repo, "add", "-A")
    commit(repo, "Add beta and edit alpha", "Body line.")

    git(repo, "mv", "alpha/one.md", "alpha/renamed.md")
    commit(repo, "Rename one", "")
    return repo


def test_full_read_then_nothing_new(tmp_path: Path) -> None:
    repo = fresh_repo(tmp_path)
    store = IndexStore(str(tmp_path / "index.sqlite"))
    first = read_git(str(repo), "master", "")
    assert len(first.records) == 3
    assert first.complete
    assert len(first.all_ids) == 3
    assert first.cursor.startswith("master:")
    report = apply_read(store, "git", first, "2026-11-18T10:00:00+00:00", 0.0)
    assert report.new == 3
    assert report.retracted == 0

    again = read_git(str(repo), "master", first.cursor)
    assert len(again.records) == 0
    quiet = apply_read(store, "git", again, "2026-11-18T10:01:00+00:00", 0.0)
    assert quiet.new == 0
    assert quiet.versions == 0
    assert quiet.retracted == 0


def test_commit_record_text_and_links(tmp_path: Path) -> None:
    repo = fresh_repo(tmp_path)
    first = read_git(str(repo), "master", "")
    middle = first.records[1]
    assert middle.kind == "commit"
    assert middle.title == "Add beta and edit alpha"
    assert not middle.trusted
    assert len(middle.id) == 10
    assert "Body line." in middle.text
    assert "commit " + middle.id in middle.text
    assert "Changed 2 files, +2 -0" in middle.text
    assert "  beta/two.md  +1 -0" in middle.text
    # Links: the parent's id, then one project per top-level folder touched.
    assert any(link.startswith("project:alpha") for link in middle.links)
    assert any(link.startswith("project:beta") for link in middle.links)
    assert any(len(link) == 10 for link in middle.links)


def test_incremental_then_rewrite(tmp_path: Path) -> None:
    repo = fresh_repo(tmp_path)
    store = IndexStore(str(tmp_path / "index.sqlite"))
    first = read_git(str(repo), "master", "")
    apply_read(store, "git", first, "2026-11-18T10:00:00+00:00", 0.0)

    # A new commit is read alone: the cursor covers the rest.
    write_file(repo / "gamma" / "three.md", "three\n")
    git(repo, "add", "-A")
    commit(repo, "Add gamma", "")
    grown = read_git(str(repo), "master", first.cursor)
    assert len(grown.records) == 1
    assert not grown.complete
    assert "since" in grown.note
    added = apply_read(store, "git", grown, "2026-11-18T10:05:00+00:00", 0.0)
    assert added.new == 1
    assert added.retracted == 0

    # Amending the tip rewrites it: the cursor is no longer an ancestor, so the branch is read in
    # full. The old tip is retracted, the new one is new, and the rest are unchanged.
    git(repo, "commit", "-q", "--amend", "--no-edit", "-m", "Add-gamma-again")
    rewritten = read_git(str(repo), "master", grown.cursor)
    assert rewritten.complete
    assert len(rewritten.records) == 4
    assert "rewritten" in rewritten.note
    redo = apply_read(store, "git", rewritten, "2026-11-18T10:10:00+00:00", 0.0)
    assert redo.new == 1
    assert redo.retracted == 1
    assert count_status(store, "retracted") == 1
    assert count_status(store, "active") == 4


def test_a_missing_branch_is_an_error(tmp_path: Path) -> None:
    repo = fresh_repo(tmp_path)
    with pytest.raises(Exception):
        read_git(str(repo), "no-such-branch", "")
