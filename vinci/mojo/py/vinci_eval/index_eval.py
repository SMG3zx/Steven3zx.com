"""Lexical recall of the Mojo index on the personal question set (M3 step 2a).

Runs `vinci recall --json --limit 10 --routes lexical` once per question of
~/.vinci/eval/journal-questions-v2.json, against an index built from VINCI_HOME
(by default the staging journal). Scoring and token counting are those of
personal_baseline.py: that module scores inside run_question, which calls the
memory API, so the same expressions are repeated here on the binary's output.
The tables, the summaries and the token counter are imported from it.

Usage, from mojo/py:
    uv run python -m vinci_eval.index_eval
    uv run python -m vinci_eval.index_eval --binary ../build/vinci --home ~/.vinci/staging
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import subprocess
from pathlib import Path
from typing import Final, cast

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
ROUTE: Final = "lexical"
LIMIT: Final = 10


def run_recall(binary: Path, home: Path, query: str) -> dict[str, object]:
    """One `vinci recall --json` call against the index in home."""
    env = dict(os.environ)
    env["VINCI_HOME"] = str(home)
    completed = subprocess.run(
        [str(binary), "recall", "--json", "--limit", str(LIMIT), "--routes", ROUTE, query],
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


def print_m1_comparison(summary: RouteSummary) -> None:
    row = m1_lexical_row()
    print("\n== against M1 lexical (v2, memory API, 2026-11-16)")
    if row is None:
        print("M1 baseline file not found; no comparison printed.")
        return
    hit = cast("list[int]", row["hit"])
    abstention = cast("list[int]", row["abstention"])
    print(
        f"{'':<8} {'hit':>7} {'abst':>7} {'tok mean':>9} {'tok med':>8}\n"
        f"{'M1':<8} {str(hit[0]) + '/' + str(hit[1]):>7}"
        f" {str(abstention[0]) + '/' + str(abstention[1]):>7}"
        f" {row['tokens_mean']!s:>9} {row['tokens_median']!s:>8}\n"
        f"{'2a':<8} {summary.hit.text():>7} {summary.abstention.text():>7}"
        f" {summary.tokens_mean:>9} {summary.tokens_median:>8}"
    )


def main() -> int:
    parser = argparse.ArgumentParser(prog="python -m vinci_eval.index_eval")
    parser.add_argument("--binary", type=Path, default=DEFAULT_BINARY)
    parser.add_argument("--home", type=Path, default=DEFAULT_HOME)
    args = parser.parse_args()
    binary: Path = args.binary
    home: Path = args.home

    questions = load_questions(QUESTIONS)
    mode = "llama-server" if tokenizer_reachable() else "len/4"
    print(f"tokens counted with: {mode}")
    print(f"binary {binary}, VINCI_HOME {home}, {len(questions)} questions")

    started = datetime.datetime.now(datetime.UTC)
    outcomes: list[Outcome] = []
    for question in questions:
        response = run_recall(binary, home, question.query)
        outcomes.append(score_question(question, response, mode))
    elapsed = (datetime.datetime.now(datetime.UTC) - started).total_seconds()

    summary = summarize((ROUTE,), outcomes)
    print_table(f"index, lexical ({len(questions)} questions)", [(ROUTE, summary)])
    print_m1_comparison(summary)
    print(f"\nrecall calls: {len(questions)} in {elapsed:.1f} s (wall, including process start)")

    report: dict[str, object] = {
        "date": started.date().isoformat(),
        "binary": str(binary),
        "vinci_home": str(home),
        "recall_limit": LIMIT,
        "token_method": mode,
        "question_set": {
            "path": str(QUESTIONS),
            "sha256": sha256_of(QUESTIONS),
            "questions": len(questions),
        },
        "routes": {ROUTE: route_json(summary)},
    }
    RESULTS_DIR.mkdir(parents=True, exist_ok=True)
    out_path = RESULTS_DIR / f"index-lexical-{started.date().isoformat()}.json"
    out_path.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"\nwrote {out_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
