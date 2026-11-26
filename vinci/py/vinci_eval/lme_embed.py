"""Embed the LongMemEval subset into the shared vector cache, for the M3 vector baseline.

Every turn of every haystack is rendered as "role: content" (as lme_baseline does), and each
question is embedded as a query with the instruction the C# side uses. Texts are deduplicated.
Keys already in the cache are skipped, so a run can be stopped and resumed. Texts the server
rejects with a 4xx (usually one over the context) are skipped, and their keys are appended to
embed-skipped.jsonl next to the cache.

    cd py && uv run python -m vinci_eval.lme_embed --count-only
    cd py && uv run python -m vinci_eval.lme_embed --base-url http://127.0.0.1:8082/v1
"""

import argparse
import json
import sys
import time
from collections.abc import Sequence
from datetime import UTC, datetime
from pathlib import Path
from typing import Final

from vinci_edge.embed import (
    BATCH_SIZE,
    DEFAULT_BASE_URL,
    DEFAULT_CACHE,
    DEFAULT_MODEL,
    EmbedCache,
    Embedder,
    EmbedRejected,
    cache_key,
    format_query,
    read_query_instruction,
)

from .lme_subset import (
    DATASET_SHA256,
    DEFAULT_DATASET,
    DEFAULT_SUBSET,
    Question,
    load_questions,
    load_subset_ids,
    sha256_file,
)

LOG_EVERY: Final = 200
CHUNK: Final = 200


def render_turn(role: str, content: str) -> str:
    return f"{role}: {content}"


def collect_texts(questions: Sequence[Question], instruction: str) -> tuple[list[str], int]:
    """Unique texts in first-seen order, plus the number of turns before deduplication."""
    unique: dict[str, None] = {}
    turns = 0
    for question in questions:
        for session in question.sessions:
            for turn in session.turns:
                turns += 1
                unique.setdefault(render_turn(turn.role, turn.content), None)
        unique.setdefault(format_query(instruction, question.question), None)
    return list(unique), turns


def _mb(chars: int) -> str:
    return f"{chars / 1_000_000:.1f} M chars"


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Embed the LongMemEval subset into the cache")
    parser.add_argument("--dataset", type=Path, default=DEFAULT_DATASET)
    parser.add_argument("--subset", type=Path, default=DEFAULT_SUBSET)
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument("--model", default=DEFAULT_MODEL)
    parser.add_argument(
        "--label", help="cache label (default: the model name); use a device label for GPU runs"
    )
    parser.add_argument("--cache", type=Path, default=DEFAULT_CACHE)
    parser.add_argument("--limit", type=int, help="only the first N questions of the subset")
    parser.add_argument("--count-only", action="store_true", help="count texts and stop")
    args = parser.parse_args(argv)

    digest = sha256_file(args.dataset)
    if digest != DATASET_SHA256:
        print(f"dataset sha256 {digest} does not match {DATASET_SHA256}", file=sys.stderr)
        return 1
    by_id = {q.question_id: q for q in load_questions(args.dataset)}
    subset_ids = load_subset_ids(args.subset)
    if args.limit is not None:
        subset_ids = subset_ids[: args.limit]
    questions = [by_id[qid] for qid in subset_ids]

    instruction = read_query_instruction()
    texts, turns = collect_texts(questions, instruction)
    chars = sum(len(text) for text in texts)
    print(
        f"questions {len(questions)}, turns {turns}, unique texts {len(texts)} "
        f"(queries {len(questions)}), characters {chars} ({_mb(chars)})"
    )
    print(f"query instruction: {instruction!r}")
    if args.count_only:
        return 0

    label = args.label if args.label is not None else args.model
    embedder = Embedder(args.base_url, args.model, EmbedCache(args.cache), instruction, label=label)
    cache = embedder.cache
    cached = cache.get_many([cache_key(label, text) for text in texts])
    pending = [text for text in texts if cache_key(label, text) not in cached]
    pending_chars = sum(len(text) for text in pending)
    print(
        f"cache {cache.path}: {len(cached)} already cached, {len(pending)} to embed "
        f"({_mb(pending_chars)}) via {args.base_url}"
    )

    skipped_path = cache.path.parent / "embed-skipped.jsonl"
    skipped: list[str] = []
    done = 0
    start = time.monotonic()
    last_log = 0

    for offset in range(0, len(pending), CHUNK):
        chunk = pending[offset : offset + CHUNK]
        try:
            embedder.embed(chunk)
        except EmbedRejected:
            # The batch holds a text the server refuses. Batches before it are cached already,
            # so retry the rest one text at a time and skip only the refused ones.
            for text in chunk:
                try:
                    embedder.embed([text])
                except EmbedRejected as error:
                    key = cache_key(label, text)
                    skipped.append(key)
                    record = {
                        "key": key,
                        "chars": len(text),
                        "status": error.status,
                        "detail": error.detail,
                        "at": datetime.now(UTC).isoformat(),
                    }
                    with skipped_path.open("a", encoding="utf-8") as log:
                        log.write(json.dumps(record) + "\n")
                    print(f"skipped {key} ({len(text)} chars): status {error.status}", flush=True)
        done += len(chunk)
        if done - last_log >= LOG_EVERY or done == len(pending):
            last_log = done
            elapsed = time.monotonic() - start
            rate = done / elapsed if elapsed > 0 else 0.0
            remaining = (len(pending) - done) / rate if rate > 0 else float("nan")
            print(
                f"{done}/{len(pending)} texts, {elapsed / 60:.1f} min, {rate:.2f} texts/s, "
                f"ETA {remaining / 60:.1f} min, skipped {len(skipped)}",
                flush=True,
            )

    elapsed = time.monotonic() - start
    print(
        f"done: embedded {len(pending) - len(skipped)} texts in {elapsed / 60:.1f} min, "
        f"skipped {len(skipped)}, batch size {BATCH_SIZE}"
    )
    print(f"cache {cache.path}: {cache.count()} rows")
    cache.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
