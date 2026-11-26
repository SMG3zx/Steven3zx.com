"""Fixed 100-question LongMemEval subset, regenerated from the cleaned release.

Run from the mojo directory's py/ folder so the package imports:

    cd py && uv run python -m vinci_eval.lme_subset          # write eval/longmemeval-subset.json
    cd py && uv run python -m vinci_eval.lme_subset --check  # fail if the committed file differs
"""

import argparse
import hashlib
import json
import random
import sys
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Final, cast

DATASET_NAME: Final = "longmemeval_s_cleaned.json"
DATASET_URL: Final = (
    "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main/"
    "longmemeval_s_cleaned.json"
)
DATASET_SHA256: Final = "d6f21ea9d60a0d56f34a05b609c79c88a451d2ae03597821ea3d5a9678c3a442"
SEED: Final = 20261117
ABSTENTION: Final = "abstention"
ABS_SUFFIX: Final = "_abs"

# Sampled in this order from one Random(SEED). Each non-abstention group draws from
# the non-_abs ids of that question_type; the abstention group draws from all _abs ids.
QUOTAS: Final[tuple[tuple[str, int], ...]] = (
    ("temporal-reasoning", 30),
    ("multi-session", 30),
    ("knowledge-update", 15),
    ("single-session-user", 5),
    ("single-session-assistant", 5),
    ("single-session-preference", 5),
    (ABSTENTION, 10),
)

DATA_DIR: Final = Path.home() / ".vinci" / "eval" / "longmemeval"
DEFAULT_DATASET: Final = DATA_DIR / DATASET_NAME
DEFAULT_SUBSET: Final = Path(__file__).resolve().parents[1] / "eval" / "longmemeval-subset.json"


@dataclass(frozen=True)
class Turn:
    role: str
    content: str
    has_answer: bool


@dataclass(frozen=True)
class Session:
    session_id: str
    turns: tuple[Turn, ...]


@dataclass(frozen=True)
class Question:
    question_id: str
    question_type: str
    question: str
    answer_session_ids: tuple[str, ...]
    sessions: tuple[Session, ...]

    @property
    def abstention(self) -> bool:
        return self.question_id.endswith(ABS_SUFFIX)


@dataclass(frozen=True)
class Pick:
    question_id: str
    question_type: str
    group: str


@dataclass(frozen=True)
class Selection:
    picks: tuple[Pick, ...]
    pool_sizes: tuple[int, ...]


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        while True:
            chunk = handle.read(1 << 20)
            if not chunk:
                break
            digest.update(chunk)
    return digest.hexdigest()


def as_object(value: object, where: str) -> dict[str, object]:
    if not isinstance(value, dict):
        raise TypeError(f"{where}: expected a JSON object")
    return cast("dict[str, object]", value)


def _string(obj: dict[str, object], key: str, where: str) -> str:
    value = obj.get(key)
    if not isinstance(value, str):
        raise TypeError(f"{where}.{key}: expected a string")
    return value


def _string_list(obj: dict[str, object], key: str, where: str) -> tuple[str, ...]:
    value = obj.get(key)
    if not isinstance(value, list):
        raise TypeError(f"{where}.{key}: expected a list")
    items: list[str] = []
    for item in cast("list[object]", value):
        if not isinstance(item, str):
            raise TypeError(f"{where}.{key}: expected strings only")
        items.append(item)
    return tuple(items)


def _parse_turn(raw: object, where: str) -> Turn:
    obj = as_object(raw, where)
    has_answer = obj.get("has_answer", False)
    if not isinstance(has_answer, bool):
        raise TypeError(f"{where}.has_answer: expected a boolean")
    return Turn(
        role=_string(obj, "role", where),
        content=_string(obj, "content", where),
        has_answer=has_answer,
    )


def _parse_question(raw: object, where: str) -> Question:
    obj = as_object(raw, where)
    session_ids = _string_list(obj, "haystack_session_ids", where)
    sessions_raw = obj.get("haystack_sessions")
    if not isinstance(sessions_raw, list):
        raise TypeError(f"{where}.haystack_sessions: expected a list")
    session_lists = cast("list[object]", sessions_raw)
    if len(session_lists) != len(session_ids):
        raise ValueError(f"{where}: haystack_sessions and haystack_session_ids differ in length")
    sessions: list[Session] = []
    for session_id, session_raw in zip(session_ids, session_lists):
        if not isinstance(session_raw, list):
            raise TypeError(f"{where}: session {session_id} is not a list of turns")
        turns = tuple(
            _parse_turn(turn_raw, f"{where}.{session_id}[{i}]")
            for i, turn_raw in enumerate(cast("list[object]", session_raw))
        )
        sessions.append(Session(session_id=session_id, turns=turns))
    return Question(
        question_id=_string(obj, "question_id", where),
        question_type=_string(obj, "question_type", where),
        question=_string(obj, "question", where),
        answer_session_ids=_string_list(obj, "answer_session_ids", where),
        sessions=tuple(sessions),
    )


def load_questions(path: Path) -> list[Question]:
    with path.open(encoding="utf-8") as handle:
        raw: object = json.load(handle)
    if not isinstance(raw, list):
        raise TypeError(f"{path}: expected a JSON list of questions")
    return [
        _parse_question(item, f"question[{i}]") for i, item in enumerate(cast("list[object]", raw))
    ]


def select_subset(questions: Sequence[Question], seed: int = SEED) -> Selection:
    rng = random.Random(seed)
    by_id = {q.question_id: q for q in questions}
    abstention_pool = sorted(q.question_id for q in questions if q.abstention)
    picks: list[Pick] = []
    pool_sizes: list[int] = []
    for group, count in QUOTAS:
        if group == ABSTENTION:
            pool = abstention_pool
        else:
            pool = sorted(
                q.question_id for q in questions if not q.abstention and q.question_type == group
            )
        if len(pool) < count:
            raise ValueError(f"group {group}: quota {count} exceeds pool of {len(pool)}")
        pool_sizes.append(len(pool))
        for question_id in rng.sample(pool, count):
            picks.append(
                Pick(
                    question_id=question_id,
                    question_type=by_id[question_id].question_type,
                    group=group,
                )
            )
    return Selection(picks=tuple(picks), pool_sizes=tuple(pool_sizes))


def render_subset(selection: Selection, dataset_bytes: int) -> str:
    doc: dict[str, object] = {
        "description": (
            "Fixed LongMemEval subset for vinci mark 2 phase M1. Groups are sampled in "
            "QUOTAS order from random.Random(seed), each from sorted ids. Questions are "
            "listed in draw order."
        ),
        "dataset": {
            "file": DATASET_NAME,
            "sha256": DATASET_SHA256,
            "bytes": dataset_bytes,
            "source": DATASET_URL,
        },
        "seed": SEED,
        "quotas": [
            {"group": group, "count": count, "pool": pool}
            for (group, count), pool in zip(QUOTAS, selection.pool_sizes)
        ],
        "questions": [
            {"question_id": p.question_id, "question_type": p.question_type, "group": p.group}
            for p in selection.picks
        ],
    }
    return json.dumps(doc, indent=2, ensure_ascii=False) + "\n"


def load_subset_ids(path: Path) -> list[str]:
    with path.open(encoding="utf-8") as handle:
        raw: object = json.load(handle)
    obj = as_object(raw, str(path))
    items = obj.get("questions")
    if not isinstance(items, list):
        raise TypeError(f"{path}: expected a questions list")
    return [
        _string(as_object(item, f"{path}.questions[]"), "question_id", f"{path}.questions[]")
        for item in cast("list[object]", items)
    ]


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Write or check the fixed LongMemEval subset.")
    parser.add_argument("--dataset", type=Path, default=DEFAULT_DATASET)
    parser.add_argument("--out", type=Path, default=DEFAULT_SUBSET)
    parser.add_argument("--check", action="store_true", help="compare instead of writing")
    args = parser.parse_args(argv)

    digest = sha256_file(args.dataset)
    if digest != DATASET_SHA256:
        print(f"dataset sha256 {digest} does not match {DATASET_SHA256}", file=sys.stderr)
        return 1
    dataset_bytes = args.dataset.stat().st_size
    questions = load_questions(args.dataset)
    selection = select_subset(questions)
    text = render_subset(selection, dataset_bytes)

    if args.check:
        if not args.out.exists() or args.out.read_text(encoding="utf-8") != text:
            print(f"{args.out} differs from the regenerated subset", file=sys.stderr)
            return 1
        print(f"{args.out} matches the regenerated subset")
        return 0

    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(text, encoding="utf-8")
    print(f"wrote {args.out}")
    print(f"subset sha256 {hashlib.sha256(text.encode('utf-8')).hexdigest()}")
    for (group, count), pool in zip(QUOTAS, selection.pool_sizes):
        print(f"  {group:28} quota {count:3}  pool {pool:4}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
