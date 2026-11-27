"""The command line as a user sees it: `python -m vinci_cli` in a child process, against a
temporary VINCI_HOME with the tracker off. The unit tests call the functions; these check the
router, the refusals and the words the output tells the person to type."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

PY_ROOT = Path(__file__).resolve().parents[1]


def run_vinci(home: Path, *args: str) -> subprocess.CompletedProcess[str]:
    env = {
        **os.environ,
        "VINCI_HOME": str(home),
        "VINCI_NO_TRACK": "1",
        "VINCI_ENV_FILE": str(home / "empty.env"),
        "PYTHONPATH": str(PY_ROOT),
    }
    (home / "empty.env").touch()
    return subprocess.run(
        [sys.executable, "-m", "vinci_cli", *args],
        env=env,
        cwd=home,
        capture_output=True,
        text=True,
        check=False,
    )


def test_journal_idea_round_trips(tmp_path: Path) -> None:
    made = run_vinci(tmp_path, "journal", "idea", "Glider check")
    assert made.returncode == 0, made.stderr
    listed = run_vinci(tmp_path, "journal", "ideas")
    assert listed.returncode == 0, listed.stderr
    assert "Glider check" in listed.stdout


def test_bare_journal_word_is_refused_with_its_journal_form(tmp_path: Path) -> None:
    res = run_vinci(tmp_path, "idea", "Glider check")
    assert res.returncode == 1
    assert "'idea' is a journal command. Run it as 'vinci journal idea'." in res.stderr
    assert not (tmp_path / "journal.jsonl").exists(), "a refused word must write nothing"


def test_unknown_word_is_refused_and_writes_nothing(tmp_path: Path) -> None:
    res = run_vinci(tmp_path, "journal", "nosuchword")
    assert res.returncode == 1
    assert "unknown command 'nosuchword'" in res.stderr
    assert not (tmp_path / "journal.jsonl").exists()


def test_journal_without_a_word_is_a_usage_error(tmp_path: Path) -> None:
    res = run_vinci(tmp_path, "journal")
    assert res.returncode == 1
    assert "usage: vinci journal" in res.stderr


def test_recall_and_open_run_under_journal(tmp_path: Path) -> None:
    run_vinci(tmp_path, "journal", "idea", "Glider check")
    built = run_vinci(tmp_path, "index", "rebuild")
    assert built.returncode == 0, built.stderr
    recalled = run_vinci(tmp_path, "journal", "recall", "glider")
    assert recalled.returncode == 0, recalled.stderr
    opened = run_vinci(tmp_path, "journal", "open", "i1")
    assert opened.returncode == 0, opened.stderr


def test_help_lists_only_the_top_level(tmp_path: Path) -> None:
    res = run_vinci(tmp_path, "help")
    assert res.returncode == 0
    for top in ("chat", "serve", "index", "feed", "backup", "verify", "path", "help"):
        assert "vinci " + top in res.stdout
    for word in ("idea", "ideas", "recall", "ship", "watch", "sync", "memory", "--help"):
        assert "  " + word + " " not in res.stdout, word + " must not be listed"


def test_printed_hints_name_the_journal_form(tmp_path: Path) -> None:
    """A hint the person can copy must run. Seeding an idea prints the next command to type."""
    res = run_vinci(tmp_path, "journal", "idea", "Glider check")
    assert "vinci journal for i1" in res.stdout, res.stdout


def test_removed_words_are_refused(tmp_path: Path) -> None:
    for args in (("memory",), ("journal", "memory"), ("journal", "sync"), ("--help",), ("-h",)):
        res = run_vinci(tmp_path, *args)
        assert res.returncode == 1, args
        assert not (tmp_path / "journal.jsonl").exists(), args
