"""Reference retriever for the LongMemEval subset: BM25 over one FTS5 index per question.

Each question gets an in-memory SQLite FTS5 table built from its own haystack, one row per
turn. The question is lowercased, reduced to word tokens of 2+ characters outside a small
stopword list, quoted, joined with OR and matched. Sessions are ranked by their best turn.

    cd py && uv run python -m vinci_eval.lme_baseline
"""

import argparse
import json
import re
import sqlite3
import statistics
import sys
import urllib.request
from collections.abc import Callable, Iterable, Sequence
from dataclasses import dataclass
from datetime import date
from pathlib import Path
from typing import Final, cast

from .lme_subset import (
    DATASET_SHA256,
    DEFAULT_DATASET,
    DEFAULT_SUBSET,
    Question,
    as_object,
    load_questions,
    load_subset_ids,
    sha256_file,
)

TOKENIZE_URL: Final = "http://127.0.0.1:8080/tokenize"
TOKENIZE_TIMEOUT_S: Final = 60
RESULTS_DIR: Final = Path.home() / ".vinci" / "eval" / "results"
TURN_K: Final = 10
MIN_TOKEN_LEN: Final = 2
TOKEN_RE: Final = re.compile(r"[^\W_]+")
STOPWORDS: Final = frozenset(
    {
        "a", "about", "am", "an", "and", "any", "are", "as", "at", "be", "been", "but", "by",
        "can", "could", "did", "do", "does", "for", "from", "had", "has", "have", "he", "her",
        "hers", "him", "his", "how", "i", "if", "in", "into", "is", "it", "its", "me", "my",
        "no", "not", "of", "on", "or", "our", "ours", "she", "so", "than", "that", "the",
        "their", "them", "then", "there", "these", "they", "this", "those", "to", "too", "us",
        "was", "we", "were", "what", "when", "where", "which", "who", "whom", "why", "will",
        "with", "would", "you", "your", "yours",
    }
)  # fmt: skip
TYPE_ORDER: Final = (
    "temporal-reasoning",
    "multi-session",
    "knowledge-update",
    "single-session-user",
    "single-session-assistant",
    "single-session-preference",
)

TokenCounter = Callable[[str], float]


@dataclass(frozen=True)
class Hit:
    session_id: str
    turn_idx: int
    role: str
    content: str
    has_answer: bool


@dataclass(frozen=True)
class QuestionResult:
    question_id: str
    question_type: str
    abstention: bool
    n_terms: int
    n_answer_sessions: int
    session_any5: bool | None
    session_all5: bool | None
    session_any10: bool | None
    session_all10: bool | None
    turn_any10: bool | None
    context_turns: int
    tokens: float


def query_terms(question: str) -> list[str]:
    terms: list[str] = []
    for token in TOKEN_RE.findall(question.lower()):
        if len(token) >= MIN_TOKEN_LEN and token not in STOPWORDS and token not in terms:
            terms.append(token)
    return terms


def build_index(question: Question) -> sqlite3.Connection:
    conn = sqlite3.connect(":memory:")
    conn.execute(
        "CREATE VIRTUAL TABLE turns USING fts5("
        "content, session_id UNINDEXED, turn_idx UNINDEXED, role UNINDEXED, "
        "has_answer UNINDEXED)"
    )
    rows = [
        (session.session_id, idx, turn.role, turn.content, int(turn.has_answer))
        for session in question.sessions
        for idx, turn in enumerate(session.turns)
    ]
    conn.executemany(
        "INSERT INTO turns (session_id, turn_idx, role, content, has_answer) "
        "VALUES (?, ?, ?, ?, ?)",
        rows,
    )
    return conn


def retrieve(conn: sqlite3.Connection, terms: Sequence[str]) -> list[Hit]:
    if not terms:
        return []
    match = " OR ".join(f'"{term}"' for term in terms)
    rows = conn.execute(
        "SELECT session_id, turn_idx, role, content, has_answer FROM turns "
        "WHERE turns MATCH ? ORDER BY bm25(turns), rowid",
        (match,),
    ).fetchall()
    return [
        Hit(
            session_id=str(session_id),
            turn_idx=int(turn_idx),
            role=str(role),
            content=str(content),
            has_answer=bool(has_answer),
        )
        for session_id, turn_idx, role, content, has_answer in rows
    ]


def _llama_tokens(url: str, text: str) -> int:
    body = json.dumps({"content": text}).encode("utf-8")
    request = urllib.request.Request(
        url, data=body, headers={"Content-Type": "application/json"}, method="POST"
    )
    # Bypass any proxy: the server is on loopback.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(request, timeout=TOKENIZE_TIMEOUT_S) as response:
        raw: object = json.loads(response.read().decode("utf-8"))
    tokens = as_object(raw, "tokenize response").get("tokens")
    if not isinstance(tokens, list):
        raise TypeError("tokenize response has no tokens list")
    return len(cast("list[object]", tokens))


def make_token_counter(url: str) -> tuple[str, TokenCounter]:
    try:
        _llama_tokens(url, "probe")
    except (OSError, ValueError) as error:
        print(f"tokenizer: llama-server unreachable or invalid ({error}); using len/4")
        return "fallback len(text)/4 (llama-server unreachable)", lambda text: len(text) / 4

    def count(text: str) -> float:
        return float(_llama_tokens(url, text))

    return f"llama-server {url}", count


def evaluate(question: Question, count_tokens: TokenCounter) -> QuestionResult:
    conn = build_index(question)
    try:
        hits = retrieve(conn, query_terms(question.question))
    finally:
        conn.close()

    ranked = list(dict.fromkeys(hit.session_id for hit in hits))
    context_hits = hits[:TURN_K]
    tokens = count_tokens("\n".join(f"{hit.role}: {hit.content}" for hit in context_hits))

    if question.abstention:
        return QuestionResult(
            question_id=question.question_id,
            question_type=question.question_type,
            abstention=True,
            n_terms=len(query_terms(question.question)),
            n_answer_sessions=len(question.answer_session_ids),
            session_any5=None,
            session_all5=None,
            session_any10=None,
            session_all10=None,
            turn_any10=None,
            context_turns=len(context_hits),
            tokens=tokens,
        )

    answers = set(question.answer_session_ids)
    if not answers:
        raise ValueError(f"{question.question_id}: no answer sessions")
    top5 = set(ranked[:5])
    top10 = set(ranked[:10])
    has_answer_turns = any(turn.has_answer for s in question.sessions for turn in s.turns)
    turn_any10 = any(hit.has_answer for hit in context_hits) if has_answer_turns else None
    return QuestionResult(
        question_id=question.question_id,
        question_type=question.question_type,
        abstention=False,
        n_terms=len(query_terms(question.question)),
        n_answer_sessions=len(answers),
        session_any5=bool(answers & top5),
        session_all5=answers <= top5,
        session_any10=bool(answers & top10),
        session_all10=answers <= top10,
        turn_any10=turn_any10,
        context_turns=len(context_hits),
        tokens=tokens,
    )


def check_answer_sessions(questions: Iterable[Question]) -> dict[str, int]:
    checked = 0
    missing = 0
    without_answers = 0
    for question in questions:
        checked += 1
        present = {session.session_id for session in question.sessions}
        if not set(question.answer_session_ids) <= present:
            missing += 1
        if not any(turn.has_answer for s in question.sessions for turn in s.turns):
            without_answers += 1
    return {
        "questions_checked": checked,
        "answer_sessions_missing_from_haystack": missing,
        "questions_without_has_answer_turns": without_answers,
    }


def _rate(values: Sequence[bool | None]) -> float | None:
    known = [v for v in values if v is not None]
    if not known:
        return None
    return sum(known) / len(known)


def summarize(results: Sequence[QuestionResult]) -> dict[str, object]:
    recall = [r for r in results if not r.abstention]
    tokens = [r.tokens for r in results]
    return {
        "questions": len(results),
        "abstention_questions": len(results) - len(recall),
        "recall_questions": len(recall),
        "session_recall_any@5": _rate([r.session_any5 for r in recall]),
        "session_recall_all@5": _rate([r.session_all5 for r in recall]),
        "session_recall_any@10": _rate([r.session_any10 for r in recall]),
        "session_recall_all@10": _rate([r.session_all10 for r in recall]),
        "turn_recall_any@10": _rate([r.turn_any10 for r in recall]),
        "turn_questions": sum(1 for r in recall if r.turn_any10 is not None),
        "tokens_median": statistics.median(tokens) if tokens else None,
        "tokens_mean": statistics.fmean(tokens) if tokens else None,
    }


def _fmt(value: object) -> str:
    if isinstance(value, float):
        return f"{value:.1f}"
    return "-"


def _pct(value: object) -> str:
    if isinstance(value, float):
        return f"{value * 100:6.1f}%"
    return "    - "


def print_table(groups: Sequence[tuple[str, dict[str, object]]]) -> None:
    header = (
        f"{'type':27} {'n':>4} {'abs':>4} {'any@5':>7} {'all@5':>7} {'any@10':>7} "
        f"{'all@10':>7} {'turn@10':>8} {'tok med':>8} {'tok mean':>9}"
    )
    print(header)
    print("-" * len(header))
    for name, s in groups:
        print(
            f"{name:27} {s['questions']!s:>4} {s['abstention_questions']!s:>4} "
            f"{_pct(s['session_recall_any@5']):>7} {_pct(s['session_recall_all@5']):>7} "
            f"{_pct(s['session_recall_any@10']):>7} {_pct(s['session_recall_all@10']):>7} "
            f"{_pct(s['turn_recall_any@10']):>8} "
            f"{_fmt(s['tokens_median']):>8} {_fmt(s['tokens_mean']):>9}"
        )


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="LongMemEval subset baseline (BM25 over FTS5)")
    parser.add_argument("--dataset", type=Path, default=DEFAULT_DATASET)
    parser.add_argument("--subset", type=Path, default=DEFAULT_SUBSET)
    today = date.today().isoformat()
    parser.add_argument("--out", type=Path, default=RESULTS_DIR / f"lme-baseline-{today}.json")
    args = parser.parse_args(argv)

    digest = sha256_file(args.dataset)
    if digest != DATASET_SHA256:
        print(f"dataset sha256 {digest} does not match {DATASET_SHA256}", file=sys.stderr)
        return 1
    questions = load_questions(args.dataset)
    by_id = {q.question_id: q for q in questions}
    subset_ids = load_subset_ids(args.subset)
    missing = [qid for qid in subset_ids if qid not in by_id]
    if missing:
        print(f"subset ids not in dataset: {missing[:5]}", file=sys.stderr)
        return 1
    subset = [by_id[qid] for qid in subset_ids]

    tokenizer_name, count_tokens = make_token_counter(TOKENIZE_URL)
    results = [evaluate(question, count_tokens) for question in subset]
    sanity = check_answer_sessions(questions)

    groups: list[tuple[str, dict[str, object]]] = []
    for qtype in TYPE_ORDER:
        group = [r for r in results if r.question_type == qtype]
        groups.append((qtype, summarize(group)))
    overall = summarize(results)
    groups.append(("overall", overall))
    print_table(groups)
    print(f"tokenizer: {tokenizer_name}")
    print(
        "sanity: "
        f"{sanity['answer_sessions_missing_from_haystack']} answer sessions missing from "
        f"haystack across {sanity['questions_checked']} dataset questions"
    )

    doc: dict[str, object] = {
        "date": today,
        "dataset": {
            "file": args.dataset.name,
            "sha256": digest,
            "bytes": args.dataset.stat().st_size,
        },
        "subset": {
            "file": str(args.subset.name),
            "sha256": sha256_file(args.subset),
            "questions": len(subset),
        },
        "tokenizer": tokenizer_name,
        "retriever": {
            "index": "sqlite FTS5, in memory, one table per question over its haystack",
            "query": "lowercased \\w tokens (no underscore), len>=2, minus STOPWORDS, deduped, OR-joined, bm25",
            "session_rank": "first appearance in bm25 order (best turn), unmatched sessions absent",
            "turn_k": TURN_K,
            "stopwords": sorted(STOPWORDS),
        },
        "sanity": sanity,
        "per_type": {name: s for name, s in groups[:-1]},
        "overall": overall,
        "questions": [
            {
                "question_id": r.question_id,
                "question_type": r.question_type,
                "abstention": r.abstention,
                "n_terms": r.n_terms,
                "n_answer_sessions": r.n_answer_sessions,
                "session_any@5": r.session_any5,
                "session_all@5": r.session_all5,
                "session_any@10": r.session_any10,
                "session_all@10": r.session_all10,
                "turn_any@10": r.turn_any10,
                "context_turns": r.context_turns,
                "tokens": r.tokens,
            }
            for r in results
        ],
    }
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(doc, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"wrote {args.out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
