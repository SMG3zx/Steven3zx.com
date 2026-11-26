"""The feeds as a whole (M5 step a): the journal's rewrite check, the config, a reset, and the done
criterion: with every feed run once, a second run adds nothing (0 new records, 0 new versions). Each
test works in a temporary directory, never in the real VINCI_HOME."""

import subprocess
from pathlib import Path

import pytest

from vinci_core.feed import FeedConfig, load_config, reset_feed, run_feed
from vinci_core.index_build import update_from_journal
from vinci_core.index_store import IndexStore


def write_file(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def git(repo: Path, args: list[str]) -> None:
    done = subprocess.run(
        ["git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "-C", str(repo), *args],
        capture_output=True,
        text=True,
        check=False,
    )
    if done.returncode != 0:
        raise AssertionError("test git failed: " + done.stderr)


def journal_line(text: str) -> str:
    return '{"at":"2026-11-10T09:00:00-06:00","kind":"idea","id":"i1","text":"' + text + '"}\n'


def current_text_count(store: IndexStore, text: str) -> int:
    row = store.conn.execute(
        "SELECT COUNT(*) FROM facts WHERE source = 'journal' AND text = ? AND valid_until IS NULL",
        (text,),
    ).fetchone()
    return int(row[0])


def test_a_rewritten_journal_is_read_again(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    write_file(Path(journal), journal_line("good"))
    store = IndexStore(dir_ + "/index.sqlite")
    first = update_from_journal(store, journal, dir_)
    assert not first.rebuilt
    assert current_text_count(store, "good") == 1

    # Same length, different first line: the prefix hash changes, so the facts are read again.
    write_file(Path(journal), journal_line("evil"))
    rewritten = update_from_journal(store, journal, dir_)
    assert rewritten.rebuilt
    assert current_text_count(store, "good") == 0
    assert current_text_count(store, "evil") == 1

    # An append is not a rewrite: the prefix still matches, so no rebuild.
    write_file(Path(journal), journal_line("evil") + journal_line("more"))
    appended = update_from_journal(store, journal, dir_)
    assert not appended.rebuilt
    assert current_text_count(store, "more") == 1


def test_config_defaults_and_errors(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    defaults = load_config(dir_ + "/feeds.toml")
    assert defaults.git_enabled
    assert defaults.vault_enabled
    assert defaults.git_branch == "master"
    assert len(defaults.vault_roots) == 3

    write_file(Path(dir_ + "/feeds.toml"), '[git]\npath = "/somewhere"\nbogus = 1\n')
    with pytest.raises(Exception):
        load_config(dir_ + "/feeds.toml")


def test_reset_then_read_again(tmp_path: Path) -> None:
    dir_ = str(tmp_path)
    journal = dir_ + "/journal.jsonl"
    write_file(Path(journal), journal_line("good") + journal_line("second"))
    store = IndexStore(dir_ + "/index.sqlite")
    cfg = FeedConfig()
    run_feed(store, "journal", cfg, journal, dir_, "2026-11-18T10:00:00+00:00")
    before = store.fact_total()
    reset_feed(store, "journal")
    assert store.fact_total() == 0
    report = run_feed(store, "journal", cfg, journal, dir_, "2026-11-18T10:01:00+00:00")
    assert report.new == 2
    assert store.fact_total() == before


def test_every_feed_adds_nothing_on_the_second_run(tmp_path: Path) -> None:
    dir_ = tmp_path
    # A journal, a git repository and a vault, all in the temporary directory.
    journal = str(dir_ / "journal.jsonl")
    write_file(Path(journal), journal_line("good") + journal_line("second"))
    repo = dir_ / "repo"
    write_file(repo / "src" / "a.mojo", "alpha\n")
    git(repo, ["init", "-q", "-b", "master"])
    git(repo, ["add", "-A"])
    git(repo, ["commit", "-q", "-m", "First"])
    write_file(repo / "docs" / "b.md", "beta\n")
    git(repo, ["add", "-A"])
    git(repo, ["commit", "-q", "-m", "Second", "-m", "With a body."])
    write_file(dir_ / "vault" / "notes" / "plan.md", "# Plan\n\nText here.\n\n## Next\n\nMore.\n")
    write_file(dir_ / "vault" / "cold-storage" / "old.md", "# Old\n\ncold\n")

    write_file(
        dir_ / "feeds.toml",
        '[git]\npath = "' + str(repo) + '"\nbranch = "master"\n\n[vaults]\nroots = ["' + str(dir_) + '/vault"]\n',
    )
    cfg = load_config(str(dir_ / "feeds.toml"))
    store = IndexStore(str(dir_ / "index.sqlite"))
    now = "2026-11-18T10:00:00+00:00"

    names = ["journal", "git", "vaults"]
    first_new = 0
    for name in names:
        first_new += run_feed(store, name, cfg, journal, str(dir_), now).new
    assert first_new == 2 + 2 + 2

    # The done criterion: a second run of every feed adds nothing.
    for name in names:
        again = run_feed(store, name, cfg, journal, str(dir_), now)
        assert again.new == 0, name + " added records on the second run"
        assert again.versions == 0, name + " added versions on the second run"
        assert again.retracted == 0, name + " retracted on the second run"
