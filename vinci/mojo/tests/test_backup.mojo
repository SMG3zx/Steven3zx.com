# `vinci backup` (py/vinci_edge/backup_edge.py): a copy with its sha256 manifest, never an
# overwrite, pruning that removes only its own files, and a failure reported rather than raised.

from std.os import makedirs, remove
from std.pathlib import Path
from std.python import Python, PythonObject
from std.testing import assert_equal, assert_true

from embed_edge import edge_module


def root() raises -> String:
    """A folder for this run only, so two runs of the suite cannot share files.
    """
    var os = Python.import_module("os")
    return "build/test-backup-" + String(os.getpid())


def edge() raises -> PythonObject:
    _ = edge_module()
    return Python.import_module("vinci_edge.backup_edge")


def fresh_dir(name: String) raises -> String:
    var dir = root() + "/" + name
    if Path(dir).exists():
        var shutil = Python.import_module("shutil")
        _ = shutil.rmtree(PythonObject(dir))
    makedirs(dir, exist_ok=True)
    return dir


def files_in(dir: String) raises -> List[String]:
    var out = List[String]()
    for raw in Path(dir).listdir():
        out.append(String(raw))
    return out^


def test_copy_and_manifest() raises:
    var home = fresh_dir("copy")
    var journal = home + "/journal.jsonl"
    Path(journal).write_text(
        '{"at":"2026-11-19T10:00:00-06:00","kind":"idea","id":"i1"}\n'
    )
    var target = home + "/out"
    var pair = edge().backup(
        PythonObject(journal),
        PythonObject(target),
        PythonObject(0),
        PythonObject("2026-11-19T10:00:00-06:00"),
    )
    assert_equal(String(pair[0]), "ok")
    var report = String(pair[1])
    assert_true(report.find("journal-2026-11-19T16-00-00Z.jsonl") >= 0, report)
    var copy = target + "/journal-2026-11-19T16-00-00Z.jsonl"
    assert_equal(Path(copy).read_text(), Path(journal).read_text())
    var manifest = Path(copy + ".sha256").read_text()
    var digest = String(
        Python.import_module("hashlib")
        .sha256(PythonObject(Path(journal).read_text()).encode())
        .hexdigest()
    )
    assert_equal(manifest, digest + "  journal-2026-11-19T16-00-00Z.jsonl\n")


def test_never_overwrites() raises:
    var home = fresh_dir("twice")
    var journal = home + "/journal.jsonl"
    Path(journal).write_text("one\n")
    var target = home + "/out"
    var first = edge().backup(
        PythonObject(journal),
        PythonObject(target),
        PythonObject(0),
        PythonObject("2026-11-19T10:00:00-06:00"),
    )
    Path(journal).write_text("two\n")
    var second = edge().backup(
        PythonObject(journal),
        PythonObject(target),
        PythonObject(0),
        PythonObject("2026-11-19T10:00:00-06:00"),
    )
    assert_equal(String(first[0]), "ok")
    assert_equal(String(second[0]), "ok")
    assert_equal(
        Path(target + "/journal-2026-11-19T16-00-00Z.jsonl").read_text(),
        "one\n",
    )
    assert_equal(
        Path(target + "/journal-2026-11-19T16-00-00Z-1.jsonl").read_text(),
        "two\n",
    )


def test_prune_keeps_newest_and_only_backups() raises:
    var home = fresh_dir("prune")
    var journal = home + "/journal.jsonl"
    Path(journal).write_text("line\n")
    var target = home + "/out"
    makedirs(target, exist_ok=True)
    Path(target + "/notes.txt").write_text("keep me\n")
    for hour in ["10", "11", "12"]:
        var pair = edge().backup(
            PythonObject(journal),
            PythonObject(target),
            PythonObject(2),
            PythonObject("2026-11-19T" + hour + ":00:00-06:00"),
        )
        assert_equal(String(pair[0]), "ok")
    var names = files_in(target)
    var journals = 0
    var manifests = 0
    var others = 0
    for n in names:
        if n.endswith(".jsonl.sha256"):
            manifests += 1
        elif n.endswith(".jsonl") and n.startswith("journal-"):
            journals += 1
        else:
            others += 1
    assert_equal(journals, 2)
    assert_equal(manifests, 2)
    assert_equal(others, 1)
    assert_true(Path(target + "/journal-2026-11-19T17-00-00Z.jsonl").exists())
    assert_true(
        not Path(target + "/journal-2026-11-19T16-00-00Z.jsonl").exists()
    )


def test_failure_is_reported() raises:
    var home = fresh_dir("missing")
    var pair = edge().backup(
        PythonObject(home + "/nope.jsonl"),
        PythonObject(home + "/out"),
        PythonObject(0),
        PythonObject("2026-11-19T10:00:00-06:00"),
    )
    assert_equal(String(pair[0]), "error")
    assert_true(String(pair[1]).find("no journal") >= 0)


def main() raises:
    makedirs(root(), exist_ok=True)
    test_copy_and_manifest()
    test_never_overwrites()
    test_prune_keeps_newest_and_only_backups()
    test_failure_is_reported()
    var shutil = Python.import_module("shutil")
    _ = shutil.rmtree(PythonObject(root()), ignore_errors=True)
    print("test_backup: ok")
