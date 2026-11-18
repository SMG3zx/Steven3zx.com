"""Differential test for `vinci chat`: the C# oracle and the Mojo chat on the same scripted talks.

Each script (mojo/difftest/chat/*.json) is a list of standard-input lines and the model's
replies, in order, served by fake_llm.FakeLlm. Each side runs in its own temporary copy of the
fixture journal, with VINCI_HOME, HOME and the embedding and memory URLs pointed away from the
real ~/.vinci. Then:

- Where a script says "oracle": true, the standard output and the new journal lines of the
  C# binary and the Mojo binary are compared. The lines that differ by design are dropped
  first: the Neo4j notices of the C# chat, the answer-not-saved notice, and the journal kinds
  the C# chat writes to memory instead (exchange, remember, read).
- Every script's expectations are checked against the Mojo run: its output, the journal kinds
  it added, and text the model was shown.

Run from mojo/py:  uv run python -m vinci_diff.chat_diff
"""

from __future__ import annotations

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
from .oracle import ORACLE

MOJO_DIR: Final = Path(__file__).resolve().parents[2]
SCRIPT_DIR: Final = MOJO_DIR / "difftest" / "chat"
FILES_DIR: Final = SCRIPT_DIR / "files"
FIXTURE: Final = MOJO_DIR / "difftest" / "fixtures" / "mid-project.jsonl"
CANDIDATE: Final = MOJO_DIR / "build" / "vinci"
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
            "VINCI_MOJO_PY": str(MOJO_DIR / "py"),
            "VINCI_NO_TRACK": "1",
            "CHAT_BASE_URL": base_url,
            # Nothing listens on port 9: the memory service and the embedding server are down.
            "MEMORY_API_URL": "http://127.0.0.1:9",
            "MEMORY_EMBEDDED": "0",
            "EMBEDDING_BASE_URL": "http://127.0.0.1:9/v1",
        }
    )
    return env


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
    added = journal[len(fixture_lines) :]
    return Side(done.stdout, done.stderr, done.returncode, added, model)


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


def check(script: Script, root: Path) -> list[str]:
    """The reasons a script fails. Empty when it passes."""
    problems: list[str] = []
    mojo = _run_side(CANDIDATE, script, root, "mojo")
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
    for kind, want in script.expect_kinds.items():
        if counts.get(kind, 0) != want:
            problems.append(f"mojo added {counts.get(kind, 0)} {kind} lines, expected {want}")
    shown = mojo.model.contents()
    for text in script.expect_model_saw:
        if text not in shown:
            problems.append(f"the model was not shown {text!r}")
    if script.oracle:
        oracle = _run_side(ORACLE, script, root, "oracle")
        if _oracle_view(oracle.stdout) != _mojo_view(mojo.stdout):
            problems.append("stdout differs from the C# oracle (see below)")
            problems.append(_diff(_oracle_view(oracle.stdout), _mojo_view(mojo.stdout)))
        oracle_added = oracle.added
        mojo_added = [line for line in mojo.added if _kind_of(line) not in MEMORY_KINDS]
        if oracle_added != mojo_added:
            problems.append(
                f"journal lines differ from the C# oracle: oracle {oracle_added!r}, "
                f"mojo {mojo_added!r}"
            )
    return problems


def _diff(want: str, got: str) -> str:
    import difflib

    return "\n".join(
        difflib.unified_diff(want.splitlines(), got.splitlines(), "oracle", "mojo", lineterm="")
    )


def show(name: str) -> int:
    """Prints the Mojo run of one script: its standard output, the journal lines it added, and
    what the model was shown in its last request. For reading a transcript, not for a verdict."""
    scripts = load_scripts(name)
    if not scripts:
        print("no script named " + name)
        return 1
    root = Path(tempfile.mkdtemp(prefix=f"chat-show-{name}-"))
    try:
        mojo = _run_side(CANDIDATE, scripts[0], root, "mojo")
        print("--- stdout")
        print(_normalize(mojo.stdout), end="")
        print("--- journal lines added")
        for line in mojo.added:
            print(line)
        print("--- exit " + str(mojo.code))
    finally:
        shutil.rmtree(root, ignore_errors=True)
    return 0


def main(argv: list[str]) -> int:
    if len(argv) > 2 and argv[1] == "--show":
        return show(argv[2])
    only = argv[1] if len(argv) > 1 else None
    scripts = load_scripts(only)
    if not scripts:
        print("no scripts match")
        return 1
    failed = 0
    for script in scripts:
        root = Path(tempfile.mkdtemp(prefix=f"chat-diff-{script.name}-"))
        try:
            problems = check(script, root)
        finally:
            shutil.rmtree(root, ignore_errors=True)
        if problems:
            failed += 1
            print(f"FAIL {script.name}: {script.description}")
            for problem in problems:
                print(f"    {problem}")
        else:
            oracle = "compared with the oracle" if script.oracle else "Mojo only"
            print(f"PASS {script.name}: {script.description} ({oracle})")
    print(f"{len(scripts) - failed} of {len(scripts)} scripts pass")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
