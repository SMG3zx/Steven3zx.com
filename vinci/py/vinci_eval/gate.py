"""The evaluation stages of `make gate`: personal, feed and LongMemEval, against the thresholds.

Every stage works on copies. Each gets a scratch VINCI_HOME with the staging journal, index
and docs; a copy of the embedding cache or the GPU cache; and the scratch folder for its
results. Nothing under ~/.vinci is written. The routes are lexical or cached-vector, so a
question vector that is not cached fails the stage instead of being embedded (the personal
and feed stages point at a closed port; LongMemEval scores against a closed port too, and
needs the CPU server only for the corrective queries that are not cached yet).

This works only on the machine that holds the data (~/.vinci/eval and ~/.vinci/staging). When
they are missing every stage prints SKIP. Each stage prints its measured numbers beside its
thresholds, the minimum detectable difference, then one line:

    STAGE <name> PASS|FAIL|SKIP <detail>

    uv run python -m vinci_eval.gate --binary ../build/vinci --thresholds ../eval/gate-thresholds.json
"""

from __future__ import annotations

import argparse
import json
import math
import os
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Final, cast

MOJO_DIR: Final = Path(__file__).resolve().parents[2]
VINCI_DIR: Final = Path.home() / ".vinci"
STAGING: Final = VINCI_DIR / "staging"
EVAL_DIR: Final = VINCI_DIR / "eval"
EMBED_CACHE: Final = VINCI_DIR / "index" / "embed-cache.sqlite"
GPU_CACHE: Final = EVAL_DIR / "cache" / "lme-embed-gpu.sqlite"
LME_INDEX: Final = EVAL_DIR / "lme-index"
PERSONAL_QUESTIONS: Final = EVAL_DIR / "journal-questions-v2.json"
FEED_QUESTIONS: Final = MOJO_DIR / "eval" / "feed-questions.json"
LME_SUBSET: Final = MOJO_DIR / "eval" / "longmemeval-subset.json"
CPU_HEALTH: Final = "http://127.0.0.1:8081/health"
CLOSED_URL: Final = "http://127.0.0.1:9/v1"
LABEL: Final = "qwen3-embedding:4b"
# Rule of thumb from docs/mark3-plan.md section 2.7: a paired change must flip about 8 net
# questions out of 48 to be significant. Scaled by sqrt(n) to the other set sizes.
MDD_AT_48: Final = 8.0


@dataclass
class Stage:
    name: str
    status: str = "FAIL"
    detail: str = ""
    lines: list[str] = field(default_factory=list[str])

    def print(self) -> None:
        for line in self.lines:
            print(f"  {line}")
        print(f"STAGE {self.name} {self.status} {self.detail}".rstrip(), flush=True)


def mdd(n: int) -> str:
    """The minimum detectable difference for a set of n questions, as net questions and points."""
    net = max(1, round(MDD_AT_48 * math.sqrt(n / 48)))
    return f"minimum detectable difference about {net} net questions of {n} ({100 * net / n:.0f} points)"


def _json(path: Path) -> dict[str, object]:
    raw: object = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(raw, dict):
        raise TypeError(f"{path}: expected a JSON object")
    return cast("dict[str, object]", raw)


def _section(thresholds: dict[str, object], name: str) -> dict[str, object]:
    return cast("dict[str, object]", thresholds[name])


def _int(value: object) -> int:
    if isinstance(value, bool) or not isinstance(value, int | float):
        raise TypeError(f"expected a number, got {value!r}")
    return int(value)


def _pair(value: object) -> tuple[int, int]:
    items = cast("list[object]", value)
    return _int(items[0]), _int(items[1])


def _vinci_env(home: Path, scratch: Path) -> dict[str, str]:
    env = {k: v for k, v in os.environ.items() if not k.startswith("VINCI_")}
    empty = scratch / "empty.env"
    empty.write_bytes(b"")
    env.update(
        {
            "VINCI_HOME": str(home),
            "VINCI_ENV_FILE": str(empty),
            "VINCI_NO_TRACK": "1",
            "VINCI_MOJO_PY": str(MOJO_DIR / "py"),
            "LANG": "C.UTF-8",
        }
    )
    return env


def _run(argv: list[str], env: dict[str, str], cwd: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, env=env, cwd=cwd, capture_output=True, text=True, check=False)


def _copy_home(work: Path, with_index: bool) -> Path:
    home = work / "home"
    home.mkdir(parents=True)
    shutil.copy(STAGING / "journal.jsonl", home / "journal.jsonl")
    if (STAGING / "docs").is_dir():
        shutil.copytree(STAGING / "docs", home / "docs")
    if with_index and (STAGING / "index.sqlite").is_file():
        shutil.copy(STAGING / "index.sqlite", home / "index.sqlite")
    return home


def _index_eval(
    binary: Path,
    home: Path,
    scratch: Path,
    routes: str,
    cache: Path,
    questions: Path,
    sources: str | None,
) -> dict[str, object] | str:
    """Runs index_eval on the copy and returns its JSON result, or the error text."""
    out = scratch / "index-eval.json"
    argv = [
        sys.executable,
        "-m",
        "vinci_eval.index_eval",
        "--binary",
        str(binary),
        "--home",
        str(home),
        "--routes",
        routes,
        "--label",
        LABEL,
        "--base-url",
        CLOSED_URL,
        "--cache",
        str(cache),
        "--questions",
        str(questions),
        "--out",
        str(out),
    ]
    if sources is not None:
        argv += ["--sources", sources]
    done = _run(argv, _vinci_env(home, scratch), MOJO_DIR / "py")
    if done.returncode != 0:
        return "index_eval failed: " + done.stderr.strip()[-400:]
    return _json(out)


def personal(binary: Path, work: Path, th: dict[str, object]) -> Stage:
    stage = Stage("personal")
    section = _section(th, "personal")
    scratch = work / "personal"
    scratch.mkdir(parents=True)
    home = _copy_home(scratch, with_index=True)
    cache = scratch / "embed-cache.sqlite"
    shutil.copy(EMBED_CACHE, cache)
    result = _index_eval(
        binary, home, scratch, "lexical,lexical+vector", cache, PERSONAL_QUESTIONS, None
    )
    if isinstance(result, str):
        stage.detail = result
        return stage
    routes = cast("dict[str, object]", result["routes"])
    answerable, unanswerable = _int(section["answerable"]), _int(section["unanswerable"])
    parts: list[str] = []
    ok = True
    for route in ("lexical", "lexical+vector"):
        spec = cast("dict[str, object]", section[route])
        measured = cast("dict[str, object]", routes[route])
        hit = _pair(measured["hit"])
        abstain = _pair(measured["abstention"])
        hit_ok = hit[0] >= _int(spec["hit_min"])
        abstain_ok = abstain[0] >= _int(spec["abstain_min"])
        ok = ok and hit_ok and abstain_ok
        stage.lines.append(
            f"{route:15} hit {hit[0]}/{hit[1]} (min {spec['hit_min']}) "
            f"abstain {abstain[0]}/{abstain[1]} (min {spec['abstain_min']})"
        )
        parts.append(f"{route} hit {hit[0]}/{hit[1]} abstain {abstain[0]}/{abstain[1]}")
    stage.lines.append(
        f"sets: {answerable} answerable, {unanswerable} unanswerable; {mdd(answerable)}"
    )
    stage.status = "PASS" if ok else "FAIL"
    stage.detail = "; ".join(parts)
    return stage


def feed(binary: Path, work: Path, th: dict[str, object]) -> Stage:
    stage = Stage("feed")
    section = _section(th, "feed")
    scratch = work / "feed"
    scratch.mkdir(parents=True)
    home = _copy_home(scratch, with_index=False)
    env = _vinci_env(home, scratch)
    run = _run([str(binary), "feed", "run"], env, scratch)
    if run.returncode != 0:
        stage.detail = "vinci feed run failed: " + run.stderr.strip()[-400:]
        return stage
    stage.lines.append("vinci feed run: default feeds config, into the scratch index")
    cache = scratch / "unused-cache.sqlite"
    result = _index_eval(
        binary,
        home,
        scratch,
        "lexical",
        cache,
        FEED_QUESTIONS,
        _str(section["sources"]),
    )
    if isinstance(result, str):
        stage.detail = result
        return stage
    measured = cast("dict[str, object]", cast("dict[str, object]", result["routes"])["lexical"])
    hit = _pair(measured["hit"])
    minimum = _int(section["hit_min"])
    stage.lines.append(
        f"sources {section['sources']} lexical hit {hit[0]}/{hit[1]} (min {minimum})"
    )
    stage.lines.append(mdd(hit[1]))
    stage.status = "PASS" if hit[0] >= minimum else "FAIL"
    stage.detail = f"hit {hit[0]}/{hit[1]}"
    return stage


def _str(value: object) -> str:
    if not isinstance(value, str):
        raise TypeError(f"expected a string, got {value!r}")
    return value


def _cpu_up() -> bool:
    try:
        with urllib.request.urlopen(CPU_HEALTH, timeout=3) as reply:
            return reply.status == 200
    except (OSError, urllib.error.URLError):
        return False


def _subset_ids() -> list[str]:
    raw: object = json.loads(LME_SUBSET.read_text(encoding="utf-8"))
    if isinstance(raw, dict):
        raw = cast("dict[str, object]", raw).get("question_ids", [])
    return [str(x) for x in cast("list[object]", raw)]


def _indexes_ready(ids: list[str]) -> str | None:
    """None when every question has an index whose facts are journal facts, else the problem."""
    for qid in ids:
        path = LME_INDEX / f"{qid}.sqlite"
        if not path.is_file():
            return f"no index for {qid}: run lme_index_eval --prepare"
        conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
        try:
            count = conn.execute("SELECT COUNT(*) FROM facts WHERE source = 'journal'").fetchone()
        finally:
            conn.close()
        if not count or int(count[0]) == 0:
            return f"index {qid} has no journal facts: run lme_index_eval --prepare"
    return None


def lme(binary: Path, work: Path, th: dict[str, object]) -> Stage:
    stage = Stage("lme")
    section = _section(th, "lme")
    ids = _subset_ids()
    problem = _indexes_ready(ids)
    if problem is not None:
        stage.detail = problem
        return stage
    if not _cpu_up():
        stage.status = "SKIP"
        stage.detail = "the CPU embed server on 127.0.0.1:8081 is down, so the corrective queries cannot be checked"
        return stage
    scratch = work / "lme"
    scratch.mkdir(parents=True)
    gpu_copy = scratch / "lme-embed-gpu.sqlite"
    shutil.copy(GPU_CACHE, gpu_copy)
    out = scratch / "lme-index.json"
    env = {k: v for k, v in os.environ.items() if not k.startswith("VINCI_")}
    env.update(
        {
            "VINCI_LME_GPU_CACHE": str(gpu_copy),
            "VINCI_NO_TRACK": "1",
            "VINCI_ENV_FILE": str(scratch / "empty.env"),
            "VINCI_HOME": str(scratch),
            "LANG": "C.UTF-8",
        }
    )
    (scratch / "empty.env").write_bytes(b"")
    done = _run(
        [
            sys.executable,
            "-m",
            "vinci_eval.lme_index_eval",
            "--binary",
            str(binary),
            "--warm",
            "--score",
            "--out",
            str(out),
        ],
        env,
        MOJO_DIR / "py",
    )
    if done.returncode != 0 or not out.is_file():
        stage.detail = "lme_index_eval failed: " + (done.stdout + done.stderr).strip()[-400:]
        return stage
    report = _json(out)
    routes = cast("dict[str, object]", report["routes"])
    measured = cast(
        "dict[str, object]", cast("dict[str, object]", routes["lexical+vector"])["overall"]
    )
    any10 = float(str(measured["session_recall_any@10"]))
    all10 = float(str(measured["session_recall_all@10"]))
    any_min = float(str(section["any10_min"]))
    all_min = float(str(section["all10_min"]))
    recall_n = _int(measured["recall_questions"])
    tokens = float(str(measured["tokens_mean"]))
    stage.lines.append(
        f"lexical+vector any@10 {100 * any10:.1f} (min {any_min}) "
        f"all@10 {100 * all10:.1f} (min {all_min})"
    )
    stage.lines.append(f"tokens mean {tokens:.0f} over the top 10; {mdd(recall_n)}")
    ok = 100 * any10 >= any_min and 100 * all10 >= all_min
    stage.status = "PASS" if ok else "FAIL"
    stage.detail = f"any@10 {100 * any10:.1f} all@10 {100 * all10:.1f}"
    return stage


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="python -m vinci_eval.gate")
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument(
        "--thresholds", type=Path, default=MOJO_DIR / "eval" / "gate-thresholds.json"
    )
    parser.add_argument("--only", default="personal,feed,lme", help="stages to run")
    args = parser.parse_args(argv)
    th = _json(args.thresholds)
    wanted = [s for s in args.only.split(",") if s]
    if not (EVAL_DIR.is_dir() and STAGING.is_dir()):
        for name in wanted:
            print(
                f"STAGE {name} SKIP evals: data missing (needs ~/.vinci/eval and ~/.vinci/staging)"
            )
        print("SKIP evals: data missing", flush=True)
        return 3
    stages: dict[str, Callable[[Path, Path, dict[str, object]], Stage]] = {
        "personal": personal,
        "feed": feed,
        "lme": lme,
    }
    failed = 0
    with tempfile.TemporaryDirectory(prefix="vinci-gate-eval-") as tmp:
        work = Path(tmp)
        for name in wanted:
            stage = stages[name](args.binary, work, th)
            stage.print()
            if stage.status != "PASS":
                failed += 1
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
