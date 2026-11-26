"""Differential test for `vinci chat`: the C# oracle and the Mojo chat on the same scripted talks.

Each script (py/difftest/chat/*.json) is a list of standard-input lines and the model's
replies, in order, served by fake_llm.FakeLlm. Each side runs in its own temporary copy of the
fixture journal, with VINCI_HOME, HOME and the embedding and memory URLs pointed away from the
real ~/.vinci. Then:

- Where a script says "oracle": true, the standard output and the new journal lines of the
  C# binary and the Mojo binary are compared. The lines that differ by design are dropped
  first: the Neo4j notices of the C# chat, the answer-not-saved notice, and the journal kinds
  the C# chat writes to memory instead (exchange, remember, read).
- Every script's expectations are checked against the Mojo run: its output, the journal kinds
  it added, and text the model was shown.

By default the C# side comes from the goldens (py/difftest/golden/chat/, see golden.py): the
recorded standard output and journal lines of each oracle script. --oracle runs the C# binary
live. --record runs it, checks the candidate against the result, and writes the goldens only if
they agree.

Run from py:  uv run python -m vinci_diff.chat_diff [--oracle | --record] [SCRIPT]
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Final, cast

from .fake_llm import FakeLlm
from .golden import (
    GOLDEN_DIR,
    GoldenError,
    add_mode_arguments,
    mode_of,
    read_file,
    sha256_file,
    write_set,
)
from .oracle import ORACLE

PY_ROOT: Final = Path(__file__).resolve().parents[1]
SCRIPT_DIR: Final = PY_ROOT / "difftest" / "chat"
FILES_DIR: Final = SCRIPT_DIR / "files"
FIXTURE: Final = PY_ROOT / "difftest" / "fixtures" / "mid-project.jsonl"
DEFAULT_CANDIDATE: Final = PY_ROOT / "vinci"
FIXED_NOW: Final = "2026-11-18T10:00:00-05:00"
TIMEOUT_S: Final = 180
# Lines the C# chat prints because it talks to Neo4j, which the Mojo chat does not use.
NEO4J_NOTICE: Final = (
    "  ! memory is not running, so exchanges are not saved (this is the only time it is said)\n"
)
# Kinds the C# chat writes to Neo4j rather than the journal, so they differ by design.
MEMORY_KINDS: Final = frozenset({"exchange", "remember", "read"})
ANSWER_NOT_SAVED: Final = "  · answer not saved: it cites no recalled evidence\n"
ENV_PREFIXES: Final = ("CHAT_", "MEMORY_", "EMBEDDING_", "VINCI_", "NEO4J_")
GOLDEN_CHAT: Final = GOLDEN_DIR / "chat"


@dataclass
class Script:
    name: str
    description: str
    stdin: list[str]
    replies: list[str]
    oracle: bool
    expect_stdout: list[str] = field(default_factory=list[str])
    expect_absent: list[str] = field(default_factory=list[str])
    expect_kinds: dict[str, int] = field(default_factory=dict[str, int])
    expect_model_saw: list[str] = field(default_factory=list[str])
    expect_exit: int = 0


@dataclass
class Side:
    stdout: str
    stderr: str
    code: int
    added: list[str]
    model: FakeLlm


def _text_list(raw: object) -> list[str]:
    if not isinstance(raw, list):
        raise TypeError("expected a list of strings in a script")
    return [str(item) for item in cast("list[object]", raw)]


def load_scripts(only: str | None) -> list[Script]:
    scripts: list[Script] = []
    for path in sorted(SCRIPT_DIR.glob("*.json")):
        raw: object = json.loads(path.read_text(encoding="utf-8"))
        if not isinstance(raw, dict):
            raise TypeError(f"{path}: a script is a JSON object")
        spec = cast("dict[str, object]", raw)
        expect = cast("dict[str, object]", spec.get("expect", {}))
        kinds_raw = expect.get("journal_kinds", {})
        kinds = {str(k): int(str(v)) for k, v in cast("dict[str, object]", kinds_raw).items()}
        script = Script(
            name=str(spec["name"]),
            description=str(spec.get("description", "")),
            stdin=_text_list(spec["stdin"]),
            replies=[_fill(text) for text in _text_list(spec["replies"])],
            oracle=bool(spec.get("oracle", False)),
            expect_stdout=_text_list(expect.get("stdout_contains", [])),
            expect_absent=_text_list(expect.get("stdout_absent", [])),
            expect_kinds=kinds,
            expect_model_saw=_text_list(expect.get("model_saw", [])),
            expect_exit=int(str(expect.get("exit", 0))),
        )
        if only is None or only == script.name:
            scripts.append(script)
    return scripts


def _fill(text: str) -> str:
    """Replaces $FILES in a reply with the fixture directory, so a read names a real file."""
    return text.replace("$FILES", str(FILES_DIR))


def _child_env(home: Path, vinci_home: Path, base_url: str, env_file: Path) -> dict[str, str]:
    env = {k: v for k, v in os.environ.items() if not k.startswith(ENV_PREFIXES)}
    env.update(
        {
            "HOME": str(home),
            "VINCI_HOME": str(vinci_home),
            "VINCI_ENV_FILE": str(env_file),
            "VINCI_NOW": FIXED_NOW,
            # The Python edge (vinci_edge) is found from here, wherever the binary is.
            "VINCI_MOJO_PY": str(PY_ROOT),
            "VINCI_NO_TRACK": "1",
            "CHAT_BASE_URL": base_url,
            # Nothing listens on port 9: the memory service and the embedding server are down.
            "MEMORY_API_URL": "http://127.0.0.1:9",
            "MEMORY_EMBEDDED": "0",
            "EMBEDDING_BASE_URL": "http://127.0.0.1:9/v1",
        }
    )
    return env


def _scrub(text: str, side_dir: Path) -> str:
    """Replaces this run's folder, the fixture folder and the repository by tokens, so a golden
    holds no path of this machine or checkout. FILES_DIR comes before PY_ROOT, which contains it."""
    for needle, token in (
        (str(side_dir), "<SIDE>"),
        (str(FILES_DIR), "<FILES>"),
        (str(PY_ROOT), "<MOJO>"),
    ):
        text = text.replace(needle, token)
    return text


def _run_side(binary: Path, script: Script, root: Path, label: str) -> Side:
    side_dir = root / label
    home = side_dir / "home"
    vinci_home = side_dir / "vinci"
    home.mkdir(parents=True)
    vinci_home.mkdir(parents=True)
    shutil.copy(FIXTURE, vinci_home / "journal.jsonl")
    env_file = side_dir / "empty.env"
    env_file.write_text("", encoding="utf-8")
    fixture_lines = FIXTURE.read_text(encoding="utf-8").splitlines()

    model = FakeLlm(script.replies)
    model.start()
    try:
        done = subprocess.run(
            [str(binary), "chat"],
            input="\n".join(script.stdin) + "\n",
            capture_output=True,
            text=True,
            env=_child_env(home, vinci_home, model.base_url, env_file),
            cwd=str(side_dir),
            timeout=TIMEOUT_S,
            check=False,
        )
    finally:
        model.stop()
    journal = (vinci_home / "journal.jsonl").read_text(encoding="utf-8").splitlines()
    added = [_scrub(line, side_dir) for line in journal[len(fixture_lines) :]]
    return Side(_scrub(done.stdout, side_dir), done.stderr, done.returncode, added, model)


def _kind_of(line: str) -> str:
    obj: object = json.loads(line)
    if isinstance(obj, dict):
        kind = cast("dict[str, object]", obj).get("kind")
        if isinstance(kind, str):
            return kind
    return ""


def _normalize(text: str) -> str:
    """Drops what differs by design and the port of the fake server, so the two runs compare."""
    text = re.sub(r"http://127\.0\.0\.1:\d+/v1", "http://FAKE/v1", text)
    text = text.replace(ANSWER_NOT_SAVED, "")
    text = text.replace(NEO4J_NOTICE, "")
    text = re.sub(r"^Memory is off:[^\n]*\n", "", text, flags=re.MULTILINE)
    text = re.sub(r"^Recorded \d+ journal facts in memory\.\n", "", text, flags=re.MULTILINE)
    return text


def _oracle_view(text: str) -> str:
    return _normalize(text)


def _mojo_view(text: str) -> str:
    return _normalize(text)


def _recorded(golden: Path, script: Script) -> tuple[str, list[str]] | str:
    """The recorded stdout and journal lines of an oracle script, or a problem as a string."""
    try:
        payload = read_file(golden, script.name)
    except (OSError, ValueError, GoldenError) as exc:
        return f"golden for {script.name} is unreadable: {exc}"
    if payload is None:
        return f"no golden for {script.name} (run --record)"
    stdout = payload.get("stdout")
    added = payload.get("journal_added")
    if not isinstance(stdout, str) or not isinstance(added, list):
        return f"golden for {script.name} has no stdout or journal_added"
    return stdout, [str(line) for line in cast("list[object]", added)]


def check(
    script: Script, root: Path, golden: Path | None, candidate: Path, oracle_bin: Path
) -> list[str]:
    """The reasons a script fails. Empty when it passes. golden is the directory of recorded
    oracle outputs, or None to run the C# binary live."""
    problems: list[str] = []
    mojo = _run_side(candidate, script, root, "mojo")
    if mojo.code != script.expect_exit:
        problems.append(f"mojo exit {mojo.code}, expected {script.expect_exit}: {mojo.stderr}")
    for text in script.expect_stdout:
        if text not in mojo.stdout:
            problems.append(f"mojo stdout lacks {text!r}")
    for text in script.expect_absent:
        if text in mojo.stdout:
            problems.append(f"mojo stdout has {text!r}")
    counts: dict[str, int] = {}
    for line in mojo.added:
        counts[_kind_of(line)] = counts.get(_kind_of(line), 0) + 1
    for kind, expected in script.expect_kinds.items():
        if counts.get(kind, 0) != expected:
            problems.append(f"mojo added {counts.get(kind, 0)} {kind} lines, expected {expected}")
    shown = mojo.model.contents()
    for text in script.expect_model_saw:
        if text not in shown:
            problems.append(f"the model was not shown {text!r}")
    if not script.oracle:
        return problems
    mojo_added = [line for line in mojo.added if _kind_of(line) not in MEMORY_KINDS]
    if golden is None:
        oracle = _run_side(oracle_bin, script, root, "oracle")
        want: tuple[str, list[str]] | str = (_oracle_view(oracle.stdout), oracle.added)
    else:
        want = _recorded(golden, script)
    if isinstance(want, str):
        return [*problems, want]
    want_stdout, want_added = want
    if want_stdout != _mojo_view(mojo.stdout):
        problems.append("stdout differs from the C# oracle (see below)")
        problems.append(_diff(want_stdout, _mojo_view(mojo.stdout)))
    if want_added != mojo_added:
        problems.append(
            f"journal lines differ from the C# oracle: oracle {want_added!r}, mojo {mojo_added!r}"
        )
    return problems


def _diff(want: str, got: str) -> str:
    import difflib

    return "\n".join(
        difflib.unified_diff(want.splitlines(), got.splitlines(), "oracle", "mojo", lineterm="")
    )


def record(only: str | None, oracle: Path, candidate: Path) -> int:
    """Runs every oracle script on the C# binary, checks the candidate against that, and writes
    the goldens only if the candidate agrees with every one of them."""
    if not oracle.is_file() or not os.access(oracle, os.X_OK):
        print(f"chat-diff: oracle is not an executable file: {oracle}", file=sys.stderr)
        return 2
    staging = Path(tempfile.mkdtemp(prefix="golden-chat-"))
    staged = staging / "chat"
    files: dict[str, dict[str, object]] = {}
    try:
        for script in load_scripts(only):
            if not script.oracle:
                continue
            root = Path(tempfile.mkdtemp(prefix=f"chat-record-{script.name}-"))
            try:
                side = _run_side(oracle, script, root, "oracle")
            finally:
                shutil.rmtree(root, ignore_errors=True)
            files[script.name] = {
                "name": script.name,
                "script_sha256": sha256_file(SCRIPT_DIR / f"{script.name}.json"),
                "stdout": _oracle_view(side.stdout),
                "journal_added": side.added,
            }
        write_set(
            staged,
            files,
            "chat",
            oracle,
            {"scripts": len(files), "normalised": "Neo4j notices, answer-not-saved, memory kinds"},
        )
        problems: list[str] = []
        for script in load_scripts(only):
            root = Path(tempfile.mkdtemp(prefix=f"chat-check-{script.name}-"))
            try:
                found = check(script, root, staged, candidate, oracle)
            finally:
                shutil.rmtree(root, ignore_errors=True)
            if found:
                problems.append(f"{script.name}:")
                problems.extend(f"    {p}" for p in found)
        if problems:
            print("\n".join(problems))
            print(
                "record refused: the candidate disagrees with the oracle or a script failed; "
                f"the goldens were not written (kept at {staged})",
                file=sys.stderr,
            )
            return 1
        if GOLDEN_CHAT.exists():
            shutil.rmtree(GOLDEN_CHAT)
        shutil.move(str(staged), str(GOLDEN_CHAT))
        print(f"wrote {len(files)} goldens to {GOLDEN_CHAT}")
        return 0
    finally:
        shutil.rmtree(staging, ignore_errors=True)


def show(name: str, candidate: Path) -> int:
    """Prints the Mojo run of one script: its standard output, the journal lines it added, and
    what the model was shown in its last request. For reading a transcript, not for a verdict."""
    scripts = load_scripts(name)
    if not scripts:
        print("no script named " + name)
        return 1
    root = Path(tempfile.mkdtemp(prefix=f"chat-show-{name}-"))
    try:
        mojo = _run_side(candidate, scripts[0], root, "mojo")
        print("--- stdout")
        print(_normalize(mojo.stdout), end="")
        print("--- journal lines added")
        for line in mojo.added:
            print(line)
        print("--- exit " + str(mojo.code))
    finally:
        shutil.rmtree(root, ignore_errors=True)
    return 0


def main(argv: list[str] | None = None) -> int:
    """Runs the harness. A golden set that does not check out exits 2 with the reason."""
    try:
        return _main(argv)
    except GoldenError as exc:
        print(f"chat-diff: {exc}", file=sys.stderr)
        return 2


def _main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="python -m vinci_diff.chat_diff")
    add_mode_arguments(parser)
    parser.add_argument("script", nargs="?", help="run only this script")
    parser.add_argument(
        "--candidate", type=Path, default=DEFAULT_CANDIDATE, help="the Mojo binary under test"
    )
    parser.add_argument(
        "--oracle-binary", type=Path, default=ORACLE, help="the C# binary for --oracle and --record"
    )
    parser.add_argument("--show", metavar="SCRIPT", help="print the Mojo run of one script")
    args = parser.parse_args(argv)
    if args.show:
        return show(str(args.show), args.candidate)
    mode = mode_of(args)
    if mode == "record":
        return record(args.script, args.oracle_binary, args.candidate)
    golden: Path | None = None
    if mode == "golden":
        if not (GOLDEN_CHAT / "_meta.json").is_file():
            print(f"chat-diff: no golden set at {GOLDEN_CHAT}: run with --record", file=sys.stderr)
            return 2
        golden = GOLDEN_CHAT
    scripts = load_scripts(args.script)
    if not scripts:
        print("no scripts match")
        return 1
    failed = 0
    for script in scripts:
        root = Path(tempfile.mkdtemp(prefix=f"chat-diff-{script.name}-"))
        try:
            problems = check(script, root, golden, args.candidate, args.oracle_binary)
        finally:
            shutil.rmtree(root, ignore_errors=True)
        if problems:
            failed += 1
            print(f"FAIL {script.name}: {script.description}")
            for problem in problems:
                print(f"    {problem}")
        else:
            if not script.oracle:
                how = "Mojo only"
            elif golden is None:
                how = "compared with the oracle"
            else:
                how = "compared with the golden"
            print(f"PASS {script.name}: {script.description} ({how})")
    print(f"{len(scripts) - failed} of {len(scripts)} scripts pass")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
