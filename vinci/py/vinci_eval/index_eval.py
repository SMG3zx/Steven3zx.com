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

Budget sweep (M3 step 3a): `--budget 1000,2000,3000,5000` runs `vinci recall --budget B`
for each budget, with the first route mix of --routes (default lexical+vector), and compares
the packed text with the unbudgeted top 10. A question is still hit when every expected
string is in the text that would be read: the packed text, or the top 10's JSON for the
unbudgeted row, as the route runs score it. Tokens are the estimator, ceil(bytes / 4), for
both, so the rows compare. The binary's own packed_tokens must agree with it.

Usage, from py:
    uv run python -m vinci_eval.index_eval
    uv run python -m vinci_eval.index_eval --routes lexical,vector,lexical+vector
    uv run python -m vinci_eval.index_eval --budget 1000,2000,3000,5000
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import statistics
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

PY_ROOT: Final = Path(__file__).resolve().parents[1]
DEFAULT_BINARY: Final = PY_ROOT / "vinci"
DEFAULT_HOME: Final = Path("~/.vinci/staging").expanduser()
QUESTIONS: Final = EVAL_DIR / "journal-questions-v2.json"
M1_RESULTS: Final = RESULTS_DIR / "personal-baseline-2026-11-16.json"
DEFAULT_ROUTES: Final = "lexical,vector,lexical+vector"
DEFAULT_LABEL: Final = "qwen3-embedding:4b"
DEFAULT_BASE_URL: Final = "http://127.0.0.1:8081/v1"
LIMIT: Final = 10
# A budget no top-10 reaches, so every record can be packed whole (the "10 whole" row).
UNLIMITED: Final = 10_000_000


def run_recall(
    binary: Path,
    home: Path,
    routes: str,
    query: str,
    label: str,
    base_url: str,
    cache: Path,
    budget: int | None = None,
    whole: int | None = None,
    sources: str | None = None,
) -> dict[str, object]:
    """One `vinci recall --json` call against the index in home. With a budget, the
    response also carries the packed context, with `whole` records in whole form.
    sources is the --sources value (journal, git, vaults, or a comma list or all); None
    leaves the binary's default, which is the journal."""
    env = dict(os.environ)
    env["VINCI_HOME"] = str(home)
    env["EMBEDDING_BASE_URL"] = base_url
    budget_args: list[str] = []
    if sources is not None:
        budget_args += ["--sources", sources]
    if budget is not None:
        budget_args += ["--budget", str(budget)]
    if whole is not None:
        budget_args += ["--whole", str(whole)]
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
            *budget_args,
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


def estimate_tokens(text: str) -> int:
    """ceil(bytes / 4): the estimator the packer uses when no tokenizer URL is given."""
    return -(-len(text.encode("utf-8")) // 4)


def int_field(response: dict[str, object], key: str) -> int:
    raw = response.get(key)
    if not isinstance(raw, int) or isinstance(raw, bool):
        raise TypeError(f"recall response: {key} must be an integer")
    return raw


def packed_text(response: dict[str, object]) -> str:
    raw = response.get("packed")
    if not isinstance(raw, str):
        raise TypeError("recall response: packed must be a string")
    return raw


def result_rows_of(response: dict[str, object]) -> list[object]:
    rows_raw = response.get("results", [])
    if not isinstance(rows_raw, list):
        raise TypeError("results must be a JSON list")
    return list(cast("list[object]", rows_raw))


def budget_sweep(
    binary: Path,
    home: Path,
    routes: str,
    questions: list[Question],
    label: str,
    base_url: str,
    cache: Path,
    budgets: list[int],
) -> dict[str, object]:
    """Unbudgeted top 10 against each budget. Prints the table and returns the report.
    routes is one route mix, with commas between its routes."""
    unbudgeted_hits = 0
    unbudgeted_tokens: list[int] = []
    unbudgeted_missed: list[str] = []
    # The top 10 with every record whole, in the packed format: the same text as the
    # budgeted rows, so the unbudgeted row compares with them on tokens.
    whole_hits = 0
    whole_tokens: list[int] = []
    whole_missed: list[str] = []
    answerable = 0
    for question in questions:
        response = run_recall(binary, home, routes, question.query, label, base_url, cache)
        rows = result_rows_of(response)
        text = result_text(rows)
        unbudgeted_tokens.append(estimate_tokens(text))
        everything = run_recall(
            binary, home, routes, question.query, label, base_url, cache, UNLIMITED, LIMIT
        )
        whole_text = packed_text(everything)
        whole_tokens.append(estimate_tokens(whole_text))
        if not question.should_abstain:
            answerable += 1
            # The same test as score_question: the lowercased JSON of the results.
            haystack = json.dumps(rows).lower()
            if all(term in haystack for term in question.expected):
                unbudgeted_hits += 1
            else:
                unbudgeted_missed.append(question.qid)
            if all(term in whole_text.lower() for term in question.expected):
                whole_hits += 1
            else:
                whole_missed.append(question.qid)

    summaries: dict[str, object] = {}
    lines: list[str] = []
    for budget in budgets:
        hits = 0
        missed: list[str] = []
        tokens: list[int] = []
        whole: list[int] = []
        pointers: list[int] = []
        omitted: list[int] = []
        oversized: list[int] = []
        for question in questions:
            response = run_recall(
                binary, home, routes, question.query, label, base_url, cache, budget
            )
            packed = packed_text(response)
            count = estimate_tokens(packed)
            if count != int_field(response, "packed_tokens"):
                raise ValueError(
                    f"{question.qid}: the binary counted {int_field(response, 'packed_tokens')}"
                    f" tokens, the estimator {count}"
                )
            if count > budget:
                raise ValueError(f"{question.qid}: packed text is {count} tokens, over {budget}")
            tokens.append(count)
            whole.append(int_field(response, "packed_whole"))
            pointers.append(int_field(response, "packed_pointers"))
            omitted.append(int_field(response, "packed_omitted"))
            oversized.append(int_field(response, "packed_oversized"))
            if not question.should_abstain:
                if all(term in packed.lower() for term in question.expected):
                    hits += 1
                else:
                    missed.append(question.qid)
        summaries[str(budget)] = {
            "hit": hits,
            "answerable": answerable,
            "missed_hits": missed,
            "tokens_mean": round(statistics.fmean(tokens), 1),
            "tokens_median": statistics.median(tokens),
            "tokens_max": max(tokens),
            "whole_mean": round(statistics.fmean(whole), 2),
            "pointers_mean": round(statistics.fmean(pointers), 2),
            "omitted_mean": round(statistics.fmean(omitted), 2),
            "oversized_total": sum(oversized),
        }
        lines.append(
            f"{budget:>8} {hits:>3}/{answerable:<3} {100 * hits / answerable:>6.1f}%"
            f" {statistics.fmean(tokens):>8.1f} {statistics.median(tokens):>7.0f}"
            f" {statistics.fmean(whole):>6.2f} {statistics.fmean(pointers):>8.2f}"
            f" {statistics.fmean(omitted):>8.2f}   missed {missed}"
        )

    print(
        f"\n== budget sweep, routes {routes}, {len(questions)} questions"
        f" ({answerable} answerable), estimator tokens"
    )
    print(
        f"{'budget':>8} {'hit':>7} {'hit %':>7} {'tok mean':>8} {'median':>7}"
        f" {'whole':>6} {'pointers':>8} {'omitted':>8}"
    )
    print(
        f"{'top 10':>8} {unbudgeted_hits:>3}/{answerable:<3}"
        f" {100 * unbudgeted_hits / answerable:>6.1f}%"
        f" {statistics.fmean(unbudgeted_tokens):>8.1f}"
        f" {statistics.median(unbudgeted_tokens):>7.0f}"
        f" {'-':>6} {'-':>8} {'-':>8}   content only, missed {unbudgeted_missed}"
    )
    print(
        f"{'10 whole':>8} {whole_hits:>3}/{answerable:<3}"
        f" {100 * whole_hits / answerable:>6.1f}%"
        f" {statistics.fmean(whole_tokens):>8.1f}"
        f" {statistics.median(whole_tokens):>7.0f}"
        f" {10:>6.2f} {0:>8.2f} {0:>8.2f}   packed, all whole, missed {whole_missed}"
    )
    for line in lines:
        print(line)
    return {
        "routes": routes,
        "label": label,
        "questions": len(questions),
        "answerable": answerable,
        "unbudgeted": {
            "hit": unbudgeted_hits,
            "missed_hits": unbudgeted_missed,
            "tokens_mean": round(statistics.fmean(unbudgeted_tokens), 1),
            "tokens_median": statistics.median(unbudgeted_tokens),
        },
        "all_whole": {
            "hit": whole_hits,
            "missed_hits": whole_missed,
            "tokens_mean": round(statistics.fmean(whole_tokens), 1),
            "tokens_median": statistics.median(whole_tokens),
        },
        "budgets": summaries,
    }


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
    parser.add_argument(
        "--sources",
        default=None,
        help="recall sources: journal (the binary's default), git, vaults, a comma list, or all",
    )
    parser.add_argument(
        "--questions",
        type=Path,
        default=QUESTIONS,
        help="question set (default: the personal v2 set)",
    )
    parser.add_argument(
        "--budget",
        help="comma-separated token budgets: run the packed-context sweep instead",
    )
    args = parser.parse_args()
    binary: Path = args.binary
    home: Path = args.home
    specs: list[str] = [spec.strip() for spec in str(args.routes).split(",") if spec.strip()]

    questions_path: Path = args.questions
    questions = load_questions(questions_path)
    sources_arg: str | None = args.sources
    if args.budget is not None:
        budgets = [int(part) for part in str(args.budget).split(",") if part.strip()]
        # One route mix for the sweep, written with "+" as --routes writes it. The default
        # --routes lists three mixes, so the sweep uses lexical+vector, the default route.
        mix = "lexical+vector" if str(args.routes) == DEFAULT_ROUTES else str(args.routes)
        routes = mix.replace("+", ",")
        started_b = datetime.datetime.now(datetime.UTC)
        report: dict[str, object] = {
            "date": started_b.date().isoformat(),
            "binary": str(binary),
            "vinci_home": str(home),
            "question_set": {
                "path": str(QUESTIONS),
                "sha256": sha256_of(QUESTIONS),
                "questions": len(questions),
            },
            "token_method": "estimate",
            "sweep": budget_sweep(
                binary, home, routes, questions, args.label, args.base_url, args.cache, budgets
            ),
        }
        out_b = args.out or (RESULTS_DIR / f"index-budget-{started_b.date().isoformat()}.json")
        out_b.parent.mkdir(parents=True, exist_ok=True)
        out_b.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
        print(f"\nwrote {out_b}")
        return 0
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
                binary,
                home,
                routes,
                question.query,
                args.label,
                args.base_url,
                args.cache,
                sources=sources_arg,
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
            "path": str(questions_path),
            "sha256": sha256_of(questions_path),
            "sources": sources_arg if sources_arg is not None else "default (journal)",
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
