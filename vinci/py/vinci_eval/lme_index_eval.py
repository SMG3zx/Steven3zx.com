"""LongMemEval subset, scored on the Mojo index (M3 step 2b).

For each question of py/eval/longmemeval-subset.json:

1. Its haystack becomes records, one per turn: source = session id, id = session id
   and turn index, at = the session date (as ISO with a +00:00 offset), kind "turn",
   text = "role: content" (lme_embed.render_turn, the text the cache was filled with),
   trusted false. They go into ~/.vinci/eval/lme-index/<question id>.sqlite by
   `vinci index load`.
2. `vinci index embed --label qwen3-embedding:4b@gpu` gives every turn its vector from
   the GPU cache. The base URL is a closed port, so a missing vector fails the run.
3. The question is recalled with `--json --limit 50` on three routes: lexical, vector
   and lexical+vector. VINCI_NOW is the question's date, since its relative dates
   were said on that day.
4. Metrics are those of lme_baseline.py: session recall any/all at 5 and 10, turn
   recall at 10, and tokens over the top 10.

Two steps need the GPU server (127.0.0.1:8082), and both are reported, not hidden:

- Corrective queries. The C# recall runs a second pass with the question's distinct
  terms when the first pass is weak (almost always, see fusion.mojo). Those query
  vectors are not in the cache lme_embed.py filled. The warm pass runs recall once per
  question against 8082, which embeds only the missing ones into the same cache under
  the same label. The count is written to the output.
- Nothing else. The scored runs use the closed port, so a cache miss there is a
  failure, not a silent server call.

The cache must hold all 46,507 rows before this runs (--allow-partial overrides it for
a dry run). It is opened read-only here, except by vinci's own embed call.

    cd py && uv run python -m vinci_eval.lme_index_eval --prepare --warm --score
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import random
import re
import sqlite3
import subprocess
import time
from collections.abc import Callable, Sequence
from pathlib import Path
from typing import Final, cast

from vinci_edge.embed import (
    DEFAULT_MODEL,
    EmbedCache,
    Embedder,
    cache_key,
    format_query,
    read_query_instruction,
)

from .lme_baseline import (
    TOKENIZE_URL,
    TYPE_ORDER,
    QuestionResult,
    make_token_counter,
    print_table,
    summarize,
)
from .lme_embed import render_turn
from .lme_subset import (
    DATASET_SHA256,
    DEFAULT_DATASET,
    DEFAULT_SUBSET,
    load_subset_ids,
    sha256_file,
)

PY_ROOT: Final = Path(__file__).resolve().parents[1]
DEFAULT_BINARY: Final = PY_ROOT / "vinci"
EVAL_DIR: Final = Path.home() / ".vinci" / "eval"
INDEX_DIR: Final = EVAL_DIR / "lme-index"
RESULTS_DIR: Final = EVAL_DIR / "results"
# The gate points this at a copy (VINCI_LME_GPU_CACHE), so the real cache is never written.
GPU_CACHE: Final = Path(
    os.environ.get("VINCI_LME_GPU_CACHE") or EVAL_DIR / "cache" / "lme-embed-gpu.sqlite"
)
LABEL: Final = "qwen3-embedding:4b@gpu"
CLOSED_URL: Final = "http://127.0.0.1:9/v1"
CPU_URL: Final = "http://127.0.0.1:8081/v1"
CPU_LABEL: Final = "qwen3-embedding:4b@cpu"
AGREEMENT_CACHE: Final = EVAL_DIR / "cache" / "agreement-cpu.sqlite"
AGREEMENT_SAMPLE: Final = 20
EXPECTED_ROWS: Final = 46507
SKIPPED_LOG: Final = EVAL_DIR / "cache" / "embed-skipped.jsonl"
LEFT_OUT: Final = re.compile(r"embed: (\d+) facts have no vector")
STAGING_HOME: Final = Path("~/.vinci/staging").expanduser()
ROUTES: Final = ("lexical", "vector", "lexical,vector")
ROUTE_NAMES: Final = {"lexical": "lexical", "vector": "vector", "lexical,vector": "lexical+vector"}
TOP_K: Final = 10
CANDIDATES: Final = 50
RECORD_TIME: Final = re.compile(r"(\d{4})/(\d{2})/(\d{2}) \(\w+\) (\d{2}):(\d{2})")


def skipped_keys() -> set[str]:
    """Distinct keys the GPU job logged as rejected (too long for the context)."""
    if not SKIPPED_LOG.exists():
        return set()
    keys: set[str] = set()
    for line in SKIPPED_LOG.read_text(encoding="utf-8").splitlines():
        if line.strip():
            keys.add(str(json.loads(line)["key"]))
    return keys


def gate_state() -> tuple[int, int]:
    """(rows in the cache with the GPU label, rows the complete cache must have)."""
    rows = cache_rows(GPU_CACHE, LABEL) if GPU_CACHE.exists() else 0
    required = EXPECTED_ROWS - len(skipped_keys())
    return rows, required


def session_of(row: dict[str, object]) -> str:
    """The LongMemEval session of a result. Records are stored under source journal, with
    id = session:turn index, so the session is the id without its last field."""
    return str(row.get("id")).rsplit(":", 1)[0]


def cache_rows(path: Path, label: str | None = None) -> int:
    """Rows in the cache, read without write access. With a label, only that label's rows:
    the completeness gate counts @gpu rows, since the CPU corrective rows share the file."""
    conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    try:
        if label is None:
            row = conn.execute("SELECT COUNT(*) FROM vectors").fetchone()
        else:
            row = conn.execute("SELECT COUNT(*) FROM vectors WHERE model = ?", (label,)).fetchone()
        return int(row[0])
    finally:
        conn.close()


def iso_of(text: str) -> str:
    """'2023/05/20 (Sat) 02:21' as '2023-05-20T02:21:00+00:00'."""
    match = RECORD_TIME.search(text)
    if match is None:
        raise ValueError(f"unrecognised LongMemEval date: {text!r}")
    year, month, day, hour, minute = match.groups()
    return f"{year}-{month}-{day}T{hour}:{minute}:00+00:00"


def vinci_env(extra: dict[str, str] | None = None) -> dict[str, str]:
    env = dict(os.environ)
    env["VINCI_HOME"] = str(STAGING_HOME)
    if extra:
        env.update(extra)
    return env


def run_vinci(
    binary: Path, args: Sequence[str], env: dict[str, str]
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [str(binary), *args], env=env, capture_output=True, text=True, check=False
    )


def question_records(question: dict[str, object]) -> list[dict[str, object]]:
    """One record per turn, as the index loader reads them."""
    session_ids = cast("list[str]", question["haystack_session_ids"])
    dates = cast("list[str]", question["haystack_dates"])
    sessions = cast("list[list[dict[str, str]]]", question["haystack_sessions"])
    records: list[dict[str, object]] = []
    for session_id, date, turns in zip(session_ids, dates, sessions, strict=True):
        at = iso_of(date)
        for index, turn in enumerate(turns):
            records.append(
                {
                    # Stored as journal facts (the default --sources), so recall finds them;
                    # the session is kept in the id (see session_of).
                    "source": "journal",
                    "id": f"{session_id}:{index}",
                    "at": at,
                    "kind": "turn",
                    "text": render_turn(turn["role"], turn["content"]),
                    "trusted": False,
                    "links": [],
                }
            )
    return records


def has_answer_turns(question: dict[str, object]) -> dict[str, bool]:
    """Turn id (session:index) to has_answer, for the turn-recall metric."""
    out: dict[str, bool] = {}
    session_ids = cast("list[str]", question["haystack_session_ids"])
    sessions = cast("list[list[dict[str, object]]]", question["haystack_sessions"])
    for session_id, turns in zip(session_ids, sessions, strict=True):
        for index, turn in enumerate(turns):
            out[f"{session_id}:{index}"] = turn.get("has_answer") is True
    return out


def prepare(binary: Path, qid: str, records: list[dict[str, object]]) -> dict[str, float]:
    """Write the records, load them into a fresh index, and embed them from the cache."""
    INDEX_DIR.mkdir(parents=True, exist_ok=True)
    jsonl = INDEX_DIR / f"{qid}.jsonl"
    sqlite_path = INDEX_DIR / f"{qid}.sqlite"
    jsonl.write_text(
        "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in records), encoding="utf-8"
    )
    if sqlite_path.exists():
        sqlite_path.unlink()
    started = time.monotonic()
    load = run_vinci(
        binary, ["index", "load", str(jsonl), "--index", str(sqlite_path)], vinci_env()
    )
    if load.returncode != 0:
        raise RuntimeError(f"{qid}: index load failed: {load.stderr.strip()}")
    loaded = time.monotonic() - started
    started = time.monotonic()
    embed = run_vinci(
        binary,
        [
            "index",
            "embed",
            "--index",
            str(sqlite_path),
            "--label",
            LABEL,
            "--base-url",
            CLOSED_URL,
            "--cache",
            str(GPU_CACHE),
        ],
        vinci_env(),
    )
    if embed.returncode != 0:
        raise RuntimeError(f"{qid}: index embed crashed: {embed.stderr.strip()}")
    match = LEFT_OUT.search(embed.stdout)
    return {
        "load_seconds": loaded,
        "embed_seconds": time.monotonic() - started,
        "facts": float(len(records)),
        "without_vector": float(match.group(1)) if match else 0.0,
    }


def recall(
    binary: Path,
    qid: str,
    query: str,
    routes: str,
    now_text: str,
    base_url: str,
    fallback_label: str = "",
    fallback_url: str = "",
) -> dict[str, object]:
    """One recall, against the question's index, at the question's date. Question
    vectors come from LABEL in the cache; a miss uses the fallback label and URL when
    they are given (see vinci_edge.index_embed.query_with_fallback)."""
    env = vinci_env({"VINCI_NOW": now_text})
    extra: list[str] = []
    if fallback_url:
        extra = ["--query-fallback-label", fallback_label, "--query-fallback-url", fallback_url]
    completed = run_vinci(
        binary,
        [
            "journal",
            "recall",
            "--json",
            "--limit",
            str(CANDIDATES),
            "--routes",
            routes,
            "--index",
            str(INDEX_DIR / f"{qid}.sqlite"),
            "--label",
            LABEL,
            "--base-url",
            base_url,
            "--cache",
            str(GPU_CACHE),
            *extra,
            query,
        ],
        env,
    )
    if completed.returncode != 0:
        raise RuntimeError(f"{qid}: recall {routes} failed: {completed.stderr.strip()[-400:]}")
    raw: object = json.loads(completed.stdout)
    if not isinstance(raw, dict):
        raise TypeError("recall --json must print an object")
    return cast("dict[str, object]", raw)


def score(
    response: dict[str, object],
    question: dict[str, object],
    qtype: str,
    abstention: bool,
    count_tokens: Callable[[str], float],
) -> QuestionResult:
    """lme_baseline.evaluate, applied to the index's ranked results."""
    results = cast("list[dict[str, object]]", response.get("results", []))
    top = results[:TOP_K]
    ranked: list[str] = []
    for row in results:
        session = session_of(row)
        if session not in ranked:
            ranked.append(session)
    text = "\n".join(str(row.get("content")) for row in top)
    token_count = count_tokens(text)
    answers = set(cast("list[str]", question["answer_session_ids"]))
    if abstention:
        return QuestionResult(
            question_id=str(question["question_id"]),
            question_type=qtype,
            abstention=True,
            n_terms=0,
            n_answer_sessions=len(answers),
            session_any5=None,
            session_all5=None,
            session_any10=None,
            session_all10=None,
            turn_any10=None,
            context_turns=len(top),
            tokens=token_count,
        )
    top5 = set(ranked[:5])
    top10 = set(ranked[:10])
    turns = has_answer_turns(question)
    turn_any10 = None
    if any(turns.values()):
        turn_any10 = any(turns.get(str(row.get("id")), False) for row in top)
    return QuestionResult(
        question_id=str(question["question_id"]),
        question_type=qtype,
        abstention=False,
        n_terms=0,
        n_answer_sessions=len(answers),
        session_any5=bool(answers & top5),
        session_all5=answers <= top5,
        session_any10=bool(answers & top10),
        session_all10=answers <= top10,
        turn_any10=turn_any10,
        context_turns=len(top),
        tokens=token_count,
    )


def corrective_for(binary: Path, qid: str, question: dict[str, object]) -> str:
    """The corrective query the index itself computes for a question ('' if none).
    Read from a lexical recall, which needs no embedding."""
    response = recall(
        binary,
        qid,
        str(question["question"]),
        "lexical",
        iso_of(str(question["question_date"])),
        CLOSED_URL,
    )
    return str(response.get("correctionQuery", ""))


def warm_cpu(
    binary: Path, subset_ids: list[str], questions: list[dict[str, object]]
) -> dict[str, int]:
    """Embed every question's corrective query on the CPU, under CPU_LABEL, into the GPU
    cache file. The GPU cache has no vector for these texts; the distinct label keeps
    CPU vectors apart from GPU ones. Documents and original questions are not touched."""
    instruction = read_query_instruction()
    queries = [
        corrective_for(binary, qid, question)
        for qid, question in zip(subset_ids, questions, strict=True)
    ]
    wrapped = sorted({format_query(instruction, q) for q in queries if q})
    cache = EmbedCache(GPU_CACHE)
    try:
        present = cache.get_many([cache_key(CPU_LABEL, t) for t in wrapped])
        needed = [t for t in wrapped if cache_key(CPU_LABEL, t) not in present]
        before = cache.count()
        if needed:
            Embedder(CPU_URL, DEFAULT_MODEL, cache, instruction, label=CPU_LABEL).embed(needed)
        added = cache.count() - before
    finally:
        cache.close()
    return {
        "questions": len(subset_ids),
        "without_correction": sum(1 for q in queries if not q),
        "distinct_corrective_queries": len(wrapped),
        "already_cached": len(wrapped) - len(needed),
        "embedded_on_cpu": len(needed),
        "rows_added": added,
    }


def agreement_sample(subset_ids: list[str], questions: list[dict[str, object]]) -> dict[str, float]:
    """Cosine between the GPU and the CPU vector of the same turn texts. The CPU vectors
    go to a separate cache file, so nothing CPU lands under the GPU label."""
    records = question_records(questions[0])
    texts = [str(r["text"]) for r in records]
    picked = random.Random(20261117).sample(texts, AGREEMENT_SAMPLE)
    gpu = EmbedCache(GPU_CACHE)
    try:
        gpu_vectors = gpu.get_many([cache_key(LABEL, t) for t in picked])
    finally:
        gpu.close()
    cpu_cache = EmbedCache(AGREEMENT_CACHE)
    try:
        cpu = Embedder(CPU_URL, DEFAULT_MODEL, cpu_cache, None, label=CPU_LABEL + "-sample")
        cpu_vectors = cpu.documents(picked)
    finally:
        cpu_cache.close()
    cosines: list[float] = []
    for text, cpu_vec in zip(picked, cpu_vectors, strict=True):
        g = gpu_vectors[cache_key(LABEL, text)]
        dot = sum(a * b for a, b in zip(g, cpu_vec, strict=True))
        na = sum(a * a for a in g) ** 0.5
        nb = sum(b * b for b in cpu_vec) ** 0.5
        cosines.append(dot / (na * nb))
    return {
        "turns": float(len(cosines)),
        "min_cosine": min(cosines),
        "mean_cosine": sum(cosines) / len(cosines),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="LongMemEval subset on the Mojo index")
    parser.add_argument("--binary", type=Path, default=DEFAULT_BINARY)
    parser.add_argument("--dataset", type=Path, default=DEFAULT_DATASET)
    parser.add_argument("--subset", type=Path, default=DEFAULT_SUBSET)
    parser.add_argument("--prepare", action="store_true", help="build and embed each index")
    parser.add_argument("--warm", action="store_true", help="embed corrective queries on the CPU")
    parser.add_argument("--agreement", action="store_true", help="GPU vs CPU cosine on a sample")
    parser.add_argument("--score", action="store_true", help="run and score the routes")
    parser.add_argument("--limit", type=int, help="only the first N questions of the subset")
    parser.add_argument("--allow-partial", action="store_true", help="skip the row-count check")
    parser.add_argument("--out", type=Path, help="JSON result file (default: the results dir)")
    args = parser.parse_args()
    binary: Path = args.binary
    if not (args.prepare or args.warm or args.score or args.agreement):
        args.prepare = args.warm = args.score = True

    rows, required = gate_state()
    print(
        f"GPU cache {GPU_CACHE}: {rows} rows, complete at {required} "
        f"({EXPECTED_ROWS} minus {len(skipped_keys())} skipped)"
    )
    if rows != required and not args.allow_partial:
        print("the cache is not complete; nothing was run")
        return 1

    digest = sha256_file(args.dataset)
    if digest != DATASET_SHA256:
        print(f"dataset sha256 {digest} does not match {DATASET_SHA256}")
        return 1
    subset_ids = load_subset_ids(args.subset)
    if args.limit is not None:
        subset_ids = subset_ids[: args.limit]
    raw_list = cast("list[dict[str, object]]", json.loads(args.dataset.read_text(encoding="utf-8")))
    by_id = {str(q["question_id"]): q for q in raw_list}
    del raw_list
    questions = [by_id[qid] for qid in subset_ids]

    rows_before_score = 0
    report: dict[str, object] = {
        "date": datetime.date.today().isoformat(),
        "label": LABEL,
        "cache": {"path": str(GPU_CACHE), "rows": rows},
        "questions": len(questions),
    }
    prep_seconds: list[dict[str, float]] = []
    without_vector: dict[str, int] = {}
    if args.prepare:
        for qid, question in zip(subset_ids, questions, strict=True):
            started = time.monotonic()
            timing = prepare(binary, qid, question_records(question))
            timing["total_seconds"] = time.monotonic() - started
            without_vector[qid] = int(timing.pop("without_vector"))
            prep_seconds.append(timing)
        report["prepare"] = {
            "turns_without_vector_total": sum(without_vector.values()),
            "turns_without_vector_per_question": without_vector,
            "questions": len(prep_seconds),
            "mean_load_seconds": sum(t["load_seconds"] for t in prep_seconds) / len(prep_seconds),
            "mean_embed_seconds": sum(t["embed_seconds"] for t in prep_seconds) / len(prep_seconds),
            "mean_total_seconds": sum(t["total_seconds"] for t in prep_seconds) / len(prep_seconds),
            "mean_facts": sum(t["facts"] for t in prep_seconds) / len(prep_seconds),
        }
        print("prepared", len(prep_seconds), "indexes", report["prepare"])

    if args.warm:
        report["warm"] = warm_cpu(binary, subset_ids, questions)
        print("warm pass (CPU, corrective queries):", report["warm"])

    if args.agreement:
        report["agreement"] = agreement_sample(subset_ids, questions)
        print("CPU vs GPU agreement on the sample:", report["agreement"])

    if args.score:
        rows_before_score = cache_rows(GPU_CACHE)
        mode_name, count_tokens = make_token_counter(TOKENIZE_URL)
        print(f"tokens counted with: {mode_name}")
        report["tokenizer"] = mode_name
        per_route: dict[str, list[QuestionResult]] = {}
        abstain_count: dict[str, int] = {}
        seconds: dict[str, float] = {}
        for routes in ROUTES:
            name = ROUTE_NAMES[routes]
            results: list[QuestionResult] = []
            abstained = 0
            begun = time.monotonic()
            for qid, question in zip(subset_ids, questions, strict=True):
                qtype = str(question["question_type"])
                response = recall(
                    binary,
                    qid,
                    str(question["question"]),
                    routes,
                    iso_of(str(question["question_date"])),
                    CLOSED_URL,
                    CPU_LABEL,
                    CLOSED_URL,
                )
                is_abstention = qid.endswith("_abs")
                if response.get("abstain") is True:
                    abstained += 1
                results.append(score(response, question, qtype, is_abstention, count_tokens))
            if not any(r.context_turns > 0 for r in results):
                raise RuntimeError(
                    f"route {name}: no question returned any result; the harness is not "
                    "reading the index (check the source of the facts, see session_of)"
                )
            seconds[name] = time.monotonic() - begun
            per_route[name] = results
            abstain_count[name] = abstained
        groups_by_route: dict[str, list[tuple[str, dict[str, object]]]] = {}
        for name, results in per_route.items():
            groups: list[tuple[str, dict[str, object]]] = []
            for qtype in TYPE_ORDER:
                group = [r for r in results if r.question_type == qtype]
                groups.append((qtype, summarize(group)))
            groups.append(("overall", summarize(results)))
            groups_by_route[name] = groups
            print(f"\n== index, {name}")
            print_table(groups)
            print(f"abstained on {abstain_count[name]} of {len(results)} questions")
        report["routes"] = {
            name: {
                "overall": summarize(results),
                "per_type": {
                    qt: summarize([r for r in results if r.question_type == qt])
                    for qt in TYPE_ORDER
                },
                "abstained": abstain_count[name],
                "seconds": seconds[name],
                "questions": [
                    {
                        "question_id": r.question_id,
                        "question_type": r.question_type,
                        "abstention": r.abstention,
                        "session_any@5": r.session_any5,
                        "session_all@5": r.session_all5,
                        "session_any@10": r.session_any10,
                        "session_all@10": r.session_all10,
                        "turn_any@10": r.turn_any10,
                        "tokens": r.tokens,
                    }
                    for r in results
                ],
            }
            for name, results in per_route.items()
        }

    if args.score:
        rows_after_score = cache_rows(GPU_CACHE)
        report["scored_runs_server_calls"] = {
            "cache_rows_before": rows_before_score,
            "cache_rows_after": rows_after_score,
            "closed_ports": [CLOSED_URL],
            "note": "every scored recall had a closed base URL and closed fallback URL; a miss would have failed the run",
        }
        print("cache rows before and after the scored runs:", rows_before_score, rows_after_score)

    out = args.out or (RESULTS_DIR / f"lme-index-{datetime.date.today().isoformat()}.json")
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(
        json.dumps(report, indent=2, ensure_ascii=False, default=str) + "\n", encoding="utf-8"
    )
    print(f"wrote {out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
