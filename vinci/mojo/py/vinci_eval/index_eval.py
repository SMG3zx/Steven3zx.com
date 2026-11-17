"""Recall of the Mojo index on the personal question set (M3 steps 2a and 2b).

Runs `vinci recall --json --limit 10 --routes <routes>` once per question of
~/.vinci/eval/journal-questions-v2.json, against an index built from VINCI_HOME
(by default the staging journal). Scoring and token counting are those of
personal_baseline.py: that module scores inside run_question, which calls the
memory API, so the same expressions are repeated here on the binary's output.
The tables, the summaries and the token counter are imported from it.

Routes are given as a list, separated by commas. A route mix joins its routes with
"+", so "lexical,vector,lexical+vector" runs three mixes. Vector routes need the
embedding settings (label, base URL, cache) that `vinci index embed` used.

Usage, from mojo/py:
    uv run python -m vinci_eval.index_eval
    uv run python -m vinci_eval.index_eval --routes lexical,vector,lexical+vector
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import subprocess
from pathlib import Path
from typing import Final, cast

from vinci_edge.embed import DEFAULT_CACHE

from .personal_baseline import (
    EVAL_DIR,
    RESULTS_DIR,
    Outcome,
    Question,
    RouteSummary,
    count_tokens,
    load_questions,
    print_table,
    result_text,
    route_json,
    sha256_of,
    summarize,
    tokenizer_reachable,
)

MOJO_DIR: Final = Path(__file__).resolve().parents[2]
DEFAULT_BINARY: Final = MOJO_DIR / "build" / "vinci"
DEFAULT_HOME: Final = Path("~/.vinci/staging").expanduser()
QUESTIONS: Final = EVAL_DIR / "journal-questions-v2.json"
M1_RESULTS: Final = RESULTS_DIR / "personal-baseline-2026-11-16.json"
DEFAULT_ROUTES: Final = "lexical,vector,lexical+vector"
DEFAULT_LABEL: Final = "qwen3-embedding:4b"
DEFAULT_BASE_URL: Final = "http://127.0.0.1:8081/v1"
LIMIT: Final = 10


def run_recall(
    binary: Path,
    home: Path,
    routes: str,
    query: str,
    label: str,
    base_url: str,
    cache: Path,
) -> dict[str, object]:
    """One `vinci recall --json` call against the index in home."""
    env = dict(os.environ)
    env["VINCI_HOME"] = str(home)
    env["EMBEDDING_BASE_URL"] = base_url
    completed = subprocess.run(
        [
            str(binary),
            "recall",
            "--json",
            "--limit",
            str(LIMIT),
            "--routes",
            routes,
            "--label",
            label,
            "--base-url",
            base_url,
            "--cache",
            str(cache),
            query,
        ],
        env=env,
        capture_output=True,
        text=True,
        check=True,
    )
    raw: object = json.loads(completed.stdout)
    if not isinstance(raw, dict):
        raise TypeError("vinci recall --json must print a JSON object")
    return cast("dict[str, object]", raw)


def score_question(question: Question, response: dict[str, object], mode: str) -> Outcome:
    """The scoring of personal_baseline.run_question, applied to a recall response."""
    rows_raw = response.get("results", [])
    if not isinstance(rows_raw, list):
        raise TypeError("results must be a JSON list")
    rows: list[object] = list(cast("list[object]", rows_raw))
    # Same test as score.py: the lowercased JSON of the results must contain every expected string.
    haystack = json.dumps(rows).lower()
    hit: bool | None = None
    if not question.should_abstain:
        hit = all(term in haystack for term in question.expected)
    abstained = response.get("abstain") is True
    return Outcome(
        qid=question.qid,
        qtype=question.qtype,
        should_abstain=question.should_abstain,
        hit=hit,
        abstained=abstained,
        abstention_ok=abstained == question.should_abstain,
        result_count=len(rows),
        tokens=count_tokens(result_text(rows), mode),
    )


def m1_lexical_row() -> dict[str, object] | None:
    """The M1 lexical row for the v2 set, from the personal baseline, if it is on disk."""
    if not M1_RESULTS.exists():
        return None
    raw: object = json.loads(M1_RESULTS.read_text(encoding="utf-8"))
    if not isinstance(raw, dict):
        return None
    sets = cast("dict[str, object]", raw).get("question_sets")
    if not isinstance(sets, dict):
        return None
    v2 = cast("dict[str, object]", sets).get("v2-60")
    if not isinstance(v2, dict):
        return None
    routes = cast("dict[str, object]", v2).get("routes")
    if not isinstance(routes, dict):
        return None
    row = cast("dict[str, object]", routes).get("lexical")
    return cast("dict[str, object]", row) if isinstance(row, dict) else None


def print_m1_comparison(summaries: list[tuple[str, RouteSummary]]) -> None:
    row = m1_lexical_row()
    print("\n== against M1 (v2, memory API, 2026-11-16)")
    if row is None:
        print("M1 baseline file not found; no comparison printed.")
        return
    hit = cast("list[int]", row["hit"])
    abstention = cast("list[int]", row["abstention"])
    print(
        f"{'':<22} {'hit':>7} {'abst':>7} {'tok mean':>9} {'tok med':>8}\n"
        f"{'M1 lexical (memory API)':<22} {str(hit[0]) + '/' + str(hit[1]):>7}"
        f" {str(abstention[0]) + '/' + str(abstention[1]):>7}"
        f" {row['tokens_mean']!s:>9} {row['tokens_median']!s:>8}"
    )
    for name, s in summaries:
        print(
            f"{'2b ' + name:<22} {s.hit.text():>7} {s.abstention.text():>7}"
            f" {s.tokens_mean:>9} {s.tokens_median:>8}"
        )


def main() -> int:
    parser = argparse.ArgumentParser(prog="python -m vinci_eval.index_eval")
    parser.add_argument("--binary", type=Path, default=DEFAULT_BINARY)
    parser.add_argument("--home", type=Path, default=DEFAULT_HOME)
    parser.add_argument(
        "--routes",
        default=DEFAULT_ROUTES,
        help="comma-separated route mixes; join routes of one mix with '+'",
    )
    parser.add_argument("--label", default=DEFAULT_LABEL)
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument("--cache", type=Path, default=DEFAULT_CACHE)
    parser.add_argument("--out", type=Path, help="JSON result file (default: results dir)")
    args = parser.parse_args()
    binary: Path = args.binary
    home: Path = args.home
    specs: list[str] = [spec.strip() for spec in str(args.routes).split(",") if spec.strip()]

    questions = load_questions(QUESTIONS)
    mode = "llama-server" if tokenizer_reachable() else "len/4"
    print(f"tokens counted with: {mode}")
    print(
        f"binary {binary}, VINCI_HOME {home}, {len(questions)} questions, "
        f"label {args.label}, cache {args.cache}"
    )

    started = datetime.datetime.now(datetime.UTC)
    summaries: list[tuple[str, RouteSummary]] = []
    timings: dict[str, float] = {}
    for spec in specs:
        routes = spec.replace("+", ",")
        outcomes: list[Outcome] = []
        begun = datetime.datetime.now(datetime.UTC)
        for question in questions:
            response = run_recall(
                binary, home, routes, question.query, args.label, args.base_url, args.cache
            )
            outcomes.append(score_question(question, response, mode))
        timings[spec] = (datetime.datetime.now(datetime.UTC) - begun).total_seconds()
        summaries.append((spec, summarize((spec,), outcomes)))

    print_table(f"index, {len(questions)} questions", summaries)
    print_m1_comparison(summaries)
    for spec, seconds in timings.items():
        print(f"recall calls for {spec}: {len(questions)} in {seconds:.1f} s (wall)")

    report: dict[str, object] = {
        "date": started.date().isoformat(),
        "binary": str(binary),
        "vinci_home": str(home),
        "recall_limit": LIMIT,
        "label": args.label,
        "token_method": mode,
        "question_set": {
            "path": str(QUESTIONS),
            "sha256": sha256_of(QUESTIONS),
            "questions": len(questions),
        },
        "routes": {spec: route_json(summary) for spec, summary in summaries},
        "seconds": timings,
    }
    out_path = args.out or (RESULTS_DIR / f"index-personal-{started.date().isoformat()}.json")
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"\nwrote {out_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
