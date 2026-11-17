"""Recall baseline over the personal journal question sets (Phase 0 of the one-journal plan).

Runs every route mix against the live memory API, scores it the way
~/.vinci/eval/score.py does, counts the tokens each recall returns, and writes
the result to ~/.vinci/eval/results/personal-baseline-<date>.json.

Standard library only. Needs the memory API on 127.0.0.1:4781 and, for token
counts, llama-server on 127.0.0.1:8080 (falls back to len/4 when it is absent).
"""

from __future__ import annotations

import datetime
import hashlib
import json
import statistics
from dataclasses import dataclass
from pathlib import Path
from typing import Final, cast
from urllib import request

MEMORY_RECALL_URL: Final = "http://127.0.0.1:4781/v1/recall"
TOKENIZE_URL: Final = "http://127.0.0.1:8080/tokenize"
EVAL_DIR: Final = Path("~/.vinci/eval").expanduser()
RESULTS_DIR: Final = EVAL_DIR / "results"
RECALL_LIMIT: Final = 10

QUESTION_SETS: Final[tuple[tuple[str, Path], ...]] = (
    ("v2-60", EVAL_DIR / "journal-questions-v2.json"),
    ("v1-26", EVAL_DIR / "journal-questions.json"),
)

# Names are the retrievalMethods values the recall service accepts (see Retrieval/Types).
ROUTES: Final[tuple[tuple[str, tuple[str, ...]], ...]] = (
    ("lexical", ("lexical",)),
    ("vector", ("vector",)),
    ("graph", ("graph",)),
    ("lexical+vector", ("lexical", "vector")),
    ("lexical+vector+graph", ("lexical", "vector", "graph")),
)


@dataclass(frozen=True)
class Question:
    qid: str
    query: str
    expected: tuple[str, ...]
    should_abstain: bool
    qtype: str


@dataclass(frozen=True)
class Outcome:
    qid: str
    qtype: str
    should_abstain: bool
    # None for abstention questions, which have no hit to score.
    hit: bool | None
    abstained: bool
    abstention_ok: bool
    result_count: int
    tokens: int


@dataclass(frozen=True)
class Tally:
    """A count of right answers out of a total, kept as a pair so JSON and print agree."""

    right: int
    total: int

    def text(self) -> str:
        return "-" if self.total == 0 else f"{self.right}/{self.total}"


@dataclass(frozen=True)
class TypeSummary:
    hit: Tally
    abstention: Tally
    missed_hits: tuple[str, ...]
    missed_abstentions: tuple[str, ...]


@dataclass(frozen=True)
class RouteSummary:
    methods: tuple[str, ...]
    hit: Tally
    abstention: Tally
    missed_hits: tuple[str, ...]
    missed_abstentions: tuple[str, ...]
    by_type: dict[str, TypeSummary]
    tokens_mean: float
    tokens_median: float
    outcomes: tuple[Outcome, ...]


def as_dict(value: object) -> dict[str, object]:
    if not isinstance(value, dict):
        raise TypeError("expected a JSON object")
    return cast("dict[str, object]", value)


def as_list(value: object) -> list[object]:
    if not isinstance(value, list):
        raise TypeError("expected a JSON list")
    return cast("list[object]", value)


def as_str(value: object, what: str) -> str:
    if not isinstance(value, str):
        raise TypeError(f"{what} must be a string")
    return value


def load_questions(path: Path) -> list[Question]:
    raw: object = json.loads(path.read_text(encoding="utf-8"))
    questions: list[Question] = []
    for item in as_list(raw):
        fields = as_dict(item)
        should = fields.get("shouldAbstain")
        if not isinstance(should, bool):
            raise TypeError(f"{path}: shouldAbstain must be a boolean")
        qtype = fields.get("type")
        questions.append(
            Question(
                qid=as_str(fields.get("id"), "id"),
                query=as_str(fields.get("query"), "query"),
                expected=tuple(
                    as_str(term, "expectedContains item")
                    for term in as_list(fields.get("expectedContains"))
                ),
                should_abstain=should,
                qtype=qtype if isinstance(qtype, str) else "untyped",
            )
        )
    return questions


def sha256_of(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def post_json(url: str, body: dict[str, object], timeout: float) -> dict[str, object]:
    req = request.Request(
        url,
        data=json.dumps(body).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with request.urlopen(req, timeout=timeout) as response:
        raw: object = json.loads(response.read().decode("utf-8"))
    return as_dict(raw)


def recall(query: str, methods: tuple[str, ...]) -> dict[str, object]:
    return post_json(
        MEMORY_RECALL_URL,
        {"query": query, "limit": RECALL_LIMIT, "retrievalMethods": list(methods)},
        timeout=120,
    )


def result_rows(response: dict[str, object]) -> list[object]:
    return as_list(response.get("results", []))


def result_text(rows: list[object]) -> str:
    """The concatenated content of the results, which is what the model would read."""
    parts: list[str] = []
    for row in rows:
        content = as_dict(row).get("content")
        if isinstance(content, str):
            parts.append(content)
    return "\n".join(parts)


def tokenizer_reachable() -> bool:
    try:
        post_json(TOKENIZE_URL, {"content": "probe"}, timeout=10)
    except OSError:
        return False
    return True


def count_tokens(text: str, mode: str) -> int:
    if mode == "len/4":
        return len(text) // 4
    return len(as_list(post_json(TOKENIZE_URL, {"content": text}, timeout=60).get("tokens")))


def run_question(question: Question, methods: tuple[str, ...], mode: str) -> Outcome:
    response = recall(question.query, methods)
    rows = result_rows(response)
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


def hit_tally(outcomes: list[Outcome]) -> Tally:
    scored = [o for o in outcomes if o.hit is not None]
    return Tally(right=sum(1 for o in scored if o.hit), total=len(scored))


def abstention_tally(outcomes: list[Outcome]) -> Tally:
    return Tally(right=sum(1 for o in outcomes if o.abstention_ok), total=len(outcomes))


def missed_hit_ids(outcomes: list[Outcome]) -> tuple[str, ...]:
    return tuple(o.qid for o in outcomes if o.hit is False)


def missed_abstention_ids(outcomes: list[Outcome]) -> tuple[str, ...]:
    return tuple(o.qid for o in outcomes if not o.abstention_ok)


def summarize(methods: tuple[str, ...], outcomes: list[Outcome]) -> RouteSummary:
    by_type: dict[str, TypeSummary] = {}
    for qtype in sorted({o.qtype for o in outcomes}):
        group = [o for o in outcomes if o.qtype == qtype]
        by_type[qtype] = TypeSummary(
            hit=hit_tally(group),
            abstention=abstention_tally(group),
            missed_hits=missed_hit_ids(group),
            missed_abstentions=missed_abstention_ids(group),
        )
    tokens = [o.tokens for o in outcomes]
    return RouteSummary(
        methods=methods,
        hit=hit_tally(outcomes),
        abstention=abstention_tally(outcomes),
        missed_hits=missed_hit_ids(outcomes),
        missed_abstentions=missed_abstention_ids(outcomes),
        by_type=by_type,
        tokens_mean=round(statistics.fmean(tokens), 1),
        tokens_median=float(statistics.median(tokens)),
        outcomes=tuple(outcomes),
    )


def tally_json(tally: Tally) -> list[int]:
    return [tally.right, tally.total]


def route_json(summary: RouteSummary) -> dict[str, object]:
    return {
        "retrievalMethods": list(summary.methods),
        "hit": tally_json(summary.hit),
        "abstention": tally_json(summary.abstention),
        "missed_hits": list(summary.missed_hits),
        "missed_abstentions": list(summary.missed_abstentions),
        "by_type": {
            qtype: {
                "hit": tally_json(t.hit),
                "abstention": tally_json(t.abstention),
                "missed_hits": list(t.missed_hits),
                "missed_abstentions": list(t.missed_abstentions),
            }
            for qtype, t in summary.by_type.items()
        },
        "tokens_mean": summary.tokens_mean,
        "tokens_median": summary.tokens_median,
        "per_question": [
            {
                "id": o.qid,
                "type": o.qtype,
                "hit": o.hit,
                "abstained": o.abstained,
                "abstention_ok": o.abstention_ok,
                "results": o.result_count,
                "tokens": o.tokens,
            }
            for o in summary.outcomes
        ],
    }


def print_table(title: str, summaries: list[tuple[str, RouteSummary]]) -> None:
    print(f"\n== {title}")
    print(f"{'route mix':<22} {'hit':>7} {'abst':>7} {'tok mean':>9} {'tok med':>8}")
    for name, s in summaries:
        print(
            f"{name:<22} {s.hit.text():>7} {s.abstention.text():>7}"
            f" {s.tokens_mean:>9} {s.tokens_median:>8}"
        )
        for qtype, t in s.by_type.items():
            print(
                f"    {qtype:<10} hit {t.hit.text():>6}  abst {t.abstention.text():>6}"
                f"  missed hits {list(t.missed_hits)}  missed abst {list(t.missed_abstentions)}"
            )


def main() -> int:
    today = datetime.date.today().isoformat()
    mode = "llama-server" if tokenizer_reachable() else "len/4"
    print(f"tokens counted with: {mode}")
    sets_out: dict[str, object] = {}
    for set_name, path in QUESTION_SETS:
        questions = load_questions(path)
        summaries: list[tuple[str, RouteSummary]] = []
        for route_name, methods in ROUTES:
            outcomes = [run_question(q, methods, mode) for q in questions]
            summaries.append((route_name, summarize(methods, outcomes)))
        print_table(f"{set_name} ({len(questions)} questions)", summaries)
        sets_out[set_name] = {
            "path": str(path),
            "sha256": sha256_of(path),
            "questions": len(questions),
            "routes": {name: route_json(s) for name, s in summaries},
        }

    report: dict[str, object] = {
        "date": today,
        "memory_recall_url": MEMORY_RECALL_URL,
        "recall_limit": RECALL_LIMIT,
        "token_method": mode,
        "token_method_note": (
            "llama-server /tokenize token count over the concatenated result contents"
            if mode == "llama-server"
            else "len/4 over the concatenated result contents (tokenizer unreachable)"
        ),
        "question_sets": sets_out,
    }
    RESULTS_DIR.mkdir(parents=True, exist_ok=True)
    out_path = RESULTS_DIR / f"personal-baseline-{today}.json"
    out_path.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"\nwrote {out_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
