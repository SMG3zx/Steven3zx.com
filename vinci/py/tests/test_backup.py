"""`vinci backup` (vinci_edge/backup_edge.py): a copy with its sha256 manifest, never an overwrite,
pruning that removes only its own files, and a failure reported rather than raised."""

import hashlib
from pathlib import Path

from vinci_edge import backup_edge

NOW = "2026-11-19T10:00:00-06:00"


def test_copy_and_manifest(tmp_path: Path) -> None:
    journal = tmp_path / "journal.jsonl"
    journal.write_text('{"at":"2026-11-19T10:00:00-06:00","kind":"idea","id":"i1"}\n', encoding="utf-8")
    target = tmp_path / "out"
    status, report = backup_edge.backup(str(journal), str(target), 0, NOW)
    assert status == "ok"
    assert "journal-2026-11-19T16-00-00Z.jsonl" in report, report
    copy = target / "journal-2026-11-19T16-00-00Z.jsonl"
    assert copy.read_text(encoding="utf-8") == journal.read_text(encoding="utf-8")
    manifest = Path(str(copy) + ".sha256").read_text(encoding="utf-8")
    digest = hashlib.sha256(journal.read_bytes()).hexdigest()
    assert manifest == digest + "  journal-2026-11-19T16-00-00Z.jsonl\n"


def test_never_overwrites(tmp_path: Path) -> None:
    journal = tmp_path / "journal.jsonl"
    journal.write_text("one\n", encoding="utf-8")
    target = tmp_path / "out"
    first = backup_edge.backup(str(journal), str(target), 0, NOW)
    journal.write_text("two\n", encoding="utf-8")
    second = backup_edge.backup(str(journal), str(target), 0, NOW)
    assert first[0] == "ok"
    assert second[0] == "ok"
    assert (target / "journal-2026-11-19T16-00-00Z.jsonl").read_text(encoding="utf-8") == "one\n"
    assert (target / "journal-2026-11-19T16-00-00Z-1.jsonl").read_text(encoding="utf-8") == "two\n"


def test_prune_keeps_newest_and_only_backups(tmp_path: Path) -> None:
    journal = tmp_path / "journal.jsonl"
    journal.write_text("line\n", encoding="utf-8")
    target = tmp_path / "out"
    target.mkdir()
    (target / "notes.txt").write_text("keep me\n", encoding="utf-8")
    for hour in ["10", "11", "12"]:
        status, _ = backup_edge.backup(str(journal), str(target), 2, "2026-11-19T" + hour + ":00:00-06:00")
        assert status == "ok"
    names = [p.name for p in target.iterdir()]
    journals = sum(1 for n in names if n.endswith(".jsonl") and n.startswith("journal-"))
    manifests = sum(1 for n in names if n.endswith(".jsonl.sha256"))
    others = sum(1 for n in names if not (n.endswith(".jsonl") or n.endswith(".jsonl.sha256")))
    assert journals == 2
    assert manifests == 2
    assert others == 1
    assert (target / "journal-2026-11-19T17-00-00Z.jsonl").exists()
    assert not (target / "journal-2026-11-19T16-00-00Z.jsonl").exists()


def test_failure_is_reported(tmp_path: Path) -> None:
    status, message = backup_edge.backup(str(tmp_path / "nope.jsonl"), str(tmp_path / "out"), 0, NOW)
    assert status == "error"
    assert "no journal" in message
