"""Answer accuracy of the real chat loop (M4 step c): `vinci chat` with its tools, on the same
questions as the budget sweep (M3 step 3b), judged the same way.

Each conversation is one run of `vinci chat` with one question on standard input, then end of
input. The chat reads its model from the llama-server on 127.0.0.1:8080, calls recall, open and
the other tools as the model asks, and prints its answer. The stdout is parsed into the answer
(tool lines and notices removed), the tool calls and the notices. The judge is the same model
with the budget sweep's prompts (budget_sweep.judge_messages).

Configurations:

- chat-w10: VINCI_CHAT_RECALL_BUDGET=3000, VINCI_CHAT_RECALL_WHOLE=10 (the chat's default).
- chat-w2: budget 1000, whole 2. The pointers need `open`, so this is the M3 open question
  again, with the tools in place.

Question sets, as budget_sweep: `lme` (the 100 fixed LongMemEval questions, each on its own index
in ~/.vinci/eval/lme-index/, with the GPU embedding cache and the CPU corrective-query fallback)
and `personal` (the 60 v2 questions on a fresh copy of ~/.vinci/staging for each conversation).
Embeddings. Document vectors come from the caches only (the base URL is closed). A question
vector is looked up in order: under the device label in the GPU cache (`gpu`), under the CPU label
in the same cache (`cpu-cached`, the corrective-query rows lme_index_eval warmed), then in a
separate scratch cache of live CPU vectors (`cpu-live`), and only then embedded live on the CPU
server at 127.0.0.1:8081 and written to that scratch cache. Nothing is written to the GPU cache or
the user's embedding cache. A recall whose vector cannot be had even on the CPU falls back to
lexical; that is recorded as `error` in the query counts, and the conversation is an error row.
The model rephrases its recall queries, so most conversations need the live path.

Eval-only hooks, all unset by default, read by the chat: VINCI_CHAT_RECALL_BUDGET and
VINCI_CHAT_RECALL_WHOLE (chat_tools.mojo), VINCI_CHAT_INDEX (the fixed index file: the chat then
never updates it from the journal; the LME runs copy the per-question index into a temporary home
and point the chat at the copy), VINCI_EVAL_EMBED_LABEL, _CACHE, _FALLBACK_LABEL and _FALLBACK_URL
(embed_edge.mojo), VINCI_CHAT_USAGE_LOG (one record per model call: token counts, the reply,
the lexical-fallback flag) and VINCI_CHAT_TEMPERATURE (0 here, as in the budget sweep; the chat
sends no temperature unless this is set). VINCI_NOW is the question's date for LME, and unset for
personal, as in the budget sweep. VINCI_NO_TRACK=1 and an empty VINCI_ENV_FILE keep the user's
memory.env out of the run. VINCI_EVAL_LIVE_CACHE and VINCI_EVAL_QUERY_LOG (vinci_edge/index_embed.py)
are the scratch cache for live CPU question vectors (--live-cache, never the GPU cache) and the
per-call source log (gpu, cpu-cached, cpu-live, error).

The model's answer is taken from stdout: the text after the question's "you> " prompt, minus the
lines that start with "  · " (tool calls and the answer-not-saved notice), "  ! " (citation
flags) and "  ? " (write confirmations). Tool calls are read from the usage log, where each
model reply that is a tool call is kept whole, and cross-checked against the notice count.

Results: one JSON line per conversation in a resumable file (default
~/.vinci/eval/results/chat-eval-<date>.jsonl). Rows with an error run again on resume.

Usage, from mojo/py:
    uv run python -m vinci_eval.chat_eval --limit-questions 3 --out SCRATCH.jsonl --transcripts DIR
    uv run python -m vinci_eval.chat_eval --set both
    uv run python -m vinci_eval.chat_eval --summary --out RESULTS.jsonl
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Final, cast

from .budget_sweep import (
    CHAT_URL,
    JUDGE_MAX_TOKENS,
    LME_TYPE_ORDER,
    Item,
    as_dict,
    chat,
    completed_keys,
    copy_staging,
    discover_model,
    format_seconds,
    judge_messages,
    lexical_hit,
    load_lme_items,
    load_personal_items,
    mean_or_dash,
    parse_verdict,
)
from .index_eval import DEFAULT_BINARY, DEFAULT_LABEL
from .lme_index_eval import CLOSED_URL, CPU_LABEL, CPU_URL, EVAL_DIR, GPU_CACHE, INDEX_DIR, LABEL
from .personal_baseline import RESULTS_DIR

LIVE_CACHE_DEFAULT: Final = EVAL_DIR / "cache" / "chat-eval-live-cpu.sqlite"
QUERY_SOURCES: Final = ("gpu", "cpu-cached", "cpu-live", "error")

CHAT_TIMEOUT_S: Final = 1200
NOT_ANSWERED_PREFIX: Final = "I could not reach an answer"
NOTICE_TOOL: Final = "  · "
NOTICE_FLAG: Final = "  ! "
NOTICE_CONFIRM: Final = "  ? "
ANSWER_NOT_SAVED: Final = "answer not saved"
OPEN_TOOL: Final = "open"
# Environment names the run controls. Any the user has set are dropped, so only the eval's hooks
# reach the chat.
DROPPED_ENV_PREFIXES: Final = ("VINCI_", "CHAT_", "EMBEDDING_", "MEMORY_", "NEO4J_")
NOTE: Final = (
    "Judge: the answering model itself (self-judging bias), as in budget_sweep. Accuracy is the "
    "judge's verdict over conversations without an error. The lexical column is every "
    "expectedContains string in the answer, over answerable personal questions. Tool columns are "
    "over conversations without an error. Tokens are the prompt and completion tokens of every "
    "model call in the turn (answer rounds only; the judge is not counted)."
)


@dataclass(frozen=True)
class ChatConfig:
    """One eval configuration: the recall packing the chat uses for each recall call."""

    name: str
    budget: int
    whole: int


CHAT_CONFIGS: Final[tuple[ChatConfig, ...]] = (
    ChatConfig("chat-w10", 3000, 10),
    ChatConfig("chat-w2", 1000, 2),
)


def config_by_name(name: str) -> ChatConfig:
    for config in CHAT_CONFIGS:
        if config.name == name:
            return config
    raise ValueError(
        f"not a chat configuration: {name!r} (use {', '.join(c.name for c in CHAT_CONFIGS)})"
    )


def row_key(set_name: str, config: str, qid: str) -> str:
    return f"{set_name}|{config}|{qid}"


def text_of(row: dict[str, object], key: str) -> str:
    value = row.get(key)
    return value if isinstance(value, str) else ""


def num_of(row: dict[str, object], key: str) -> float:
    value = row.get(key)
    return float(value) if isinstance(value, (int, float)) and not isinstance(value, bool) else 0.0


def int_of(row: dict[str, object], key: str) -> int:
    value = row.get(key)
    return value if isinstance(value, int) and not isinstance(value, bool) else 0


def conversation_env(
    home: Path,
    env_file: Path,
    usage_log: Path,
    query_log: Path,
    live_cache: Path,
    config: ChatConfig,
    model: str,
    item: Item,
    index_copy: Path | None,
) -> dict[str, str]:
    """The environment of one `vinci chat` run: the user's VINCI_, CHAT_, EMBEDDING_ and
    MEMORY_ settings are dropped, then the eval's own are set."""
    env = {
        key: value for key, value in os.environ.items() if not key.startswith(DROPPED_ENV_PREFIXES)
    }
    env.update(
        {
            "VINCI_HOME": str(home),
            "VINCI_ENV_FILE": str(env_file),
            "VINCI_NO_TRACK": "1",
            "CHAT_BASE_URL": f"{CHAT_URL}/v1",
            "CHAT_MODEL": model,
            "EMBEDDING_BASE_URL": CLOSED_URL,
            "EMBEDDING_MODEL": DEFAULT_LABEL,
            "VINCI_CHAT_RECALL_BUDGET": str(config.budget),
            "VINCI_CHAT_RECALL_WHOLE": str(config.whole),
            "VINCI_CHAT_USAGE_LOG": str(usage_log),
            "VINCI_CHAT_TEMPERATURE": "0",
            "VINCI_EVAL_QUERY_LOG": str(query_log),
            "VINCI_EVAL_LIVE_CACHE": str(live_cache),
        }
    )
    if item.now_iso is not None:
        env["VINCI_NOW"] = item.now_iso
    if item.set_name == "lme":
        if index_copy is None:
            raise ValueError("an LME conversation needs its index copy")
        env.update(
            {
                "VINCI_CHAT_INDEX": str(index_copy),
                "VINCI_EVAL_EMBED_LABEL": LABEL,
                "VINCI_EVAL_EMBED_CACHE": str(GPU_CACHE),
                "VINCI_EVAL_EMBED_FALLBACK_LABEL": CPU_LABEL,
                "VINCI_EVAL_EMBED_FALLBACK_URL": CPU_URL,
            }
        )
    else:
        # Personal: the device label is the default one (no cache override), and a query
        # missing from the default cache is embedded on the CPU under the same label.
        env.update(
            {
                "VINCI_EVAL_EMBED_FALLBACK_LABEL": DEFAULT_LABEL,
                "VINCI_EVAL_EMBED_FALLBACK_URL": CPU_URL,
            }
        )
    return env


def query_counts(path: Path) -> dict[str, int]:
    """How many question vectors came from each source, over the conversation's recalls."""
    counts = {source: 0 for source in QUERY_SOURCES}
    if not path.exists():
        return counts
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        raw: object = json.loads(line)
        source = as_dict(raw, "query record").get("source")
        if isinstance(source, str) and source in counts:
            counts[source] += 1
    return counts


def prepare_home(item: Item, home: Path) -> Path | None:
    """A fresh home for one conversation. Personal: a copy of staging. LME: an empty journal and
    a copy of the question's index, which the chat reads as a fixed file (VINCI_CHAT_INDEX).
    Returns the index copy for LME, None for personal. The originals are only read."""
    if item.set_name == "personal":
        copy_staging(home)
        return None
    home.mkdir(parents=True)
    (home / "journal.jsonl").write_text("", encoding="utf-8")
    source = INDEX_DIR / f"{item.qid}.sqlite"
    if not source.exists():
        raise FileNotFoundError(f"no index for {item.qid} at {source}")
    copy = home / "index.sqlite"
    shutil.copy2(source, copy)
    return copy


def split_stdout(stdout: str) -> tuple[str, list[str], list[str], bool]:
    """(answer, tool notice lines, citation flags, answer-not-saved) from the chat's stdout.

    The banner and pipeline output come before the first "you> " prompt. The turn is the text
    after it, up to the final "you> " that the chat prints before it reads end of input."""
    parts = stdout.split("you> ")
    turn = parts[1] if len(parts) > 1 else ""
    tools: list[str] = []
    flags: list[str] = []
    not_saved = False
    kept: list[str] = []
    for line in turn.split("\n"):
        if line.startswith(NOTICE_TOOL):
            body = line[len(NOTICE_TOOL) :]
            if body.startswith(ANSWER_NOT_SAVED):
                not_saved = True
            else:
                tools.append(body)
            continue
        if line.startswith(NOTICE_FLAG):
            flags.append(line[len(NOTICE_FLAG) :])
            continue
        if line.startswith(NOTICE_CONFIRM):
            continue
        kept.append(line)
    return ("\n".join(kept).strip(), tools, flags, not_saved)


def read_usage(path: Path) -> list[dict[str, object]]:
    if not path.exists():
        return []
    records: list[dict[str, object]] = []
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.strip():
            raw: object = json.loads(line)
            records.append(as_dict(raw, "usage record"))
    return records


def tool_calls_of(usage: list[dict[str, object]]) -> list[dict[str, object]]:
    """The model replies that were tool calls, as {"name", "args"}, in order."""
    calls: list[dict[str, object]] = []
    for record in usage:
        reply = record.get("reply")
        if not isinstance(reply, str):
            continue
        try:
            parsed: object = json.loads(reply)
        except ValueError:
            continue
        if isinstance(parsed, dict) and "tool" in parsed:
            body = cast("dict[str, object]", parsed)
            args = {key: value for key, value in body.items() if key != "tool"}
            calls.append({"name": str(body["tool"]), "args": args})
    return calls


def run_conversation(
    binary: Path,
    model: str,
    item: Item,
    config: ChatConfig,
    root: Path,
    live_cache: Path,
    transcripts: Path | None,
) -> dict[str, object]:
    """One conversation, and its judge. Failures become the row's error, never a raise."""
    row: dict[str, object] = {
        "set": item.set_name,
        "config": config.name,
        "qid": item.qid,
        "qtype": item.qtype,
        "abstention": item.abstention,
        "question": item.question,
        "model": model,
        "run_at": datetime.datetime.now(datetime.UTC).isoformat(timespec="seconds"),
        "error": None,
        "budget": config.budget,
        "whole": config.whole,
    }
    started = time.monotonic()
    try:
        with tempfile.TemporaryDirectory(prefix="chat-", dir=root) as raw_tmp:
            tmp = Path(raw_tmp)
            home = tmp / "home"
            index_copy = prepare_home(item, home)
            env_file = tmp / "empty.env"
            env_file.write_text("", encoding="utf-8")
            usage_log = tmp / "usage.jsonl"
            query_log = tmp / "queries.jsonl"
            env = conversation_env(
                home, env_file, usage_log, query_log, live_cache, config, model, item, index_copy
            )
            question = item.question.replace("\n", " ")
            completed = subprocess.run(
                [str(binary), "chat"],
                input=question + "\n",
                capture_output=True,
                text=True,
                env=env,
                timeout=CHAT_TIMEOUT_S,
                check=False,
            )
            chat_seconds = time.monotonic() - started
            usage = read_usage(usage_log)
            sources = query_counts(query_log)
        if transcripts is not None:
            transcripts.mkdir(parents=True, exist_ok=True)
            name = f"{item.set_name}-{config.name}-{item.qid}.txt"
            (transcripts / name).write_text(
                completed.stdout + "\n--- stderr ---\n" + completed.stderr, encoding="utf-8"
            )
        if completed.returncode != 0:
            tail = completed.stderr.strip()[-300:]
            raise RuntimeError(f"vinci chat exited {completed.returncode}: {tail}")
        answer, notices, flags, not_saved = split_stdout(completed.stdout)
        calls = tool_calls_of(usage)
        prompt_tokens = sum(int_of(r, "prompt_tokens") for r in usage)
        completion_tokens = sum(int_of(r, "completion_tokens") for r in usage)
        failed = sources["error"]
        row.update(
            {
                "query_sources": sources,
                "answer": answer,
                "not_answered": answer.startswith(NOT_ANSWERED_PREFIX),
                "tool_calls": calls,
                "tool_call_count": len(calls),
                "tool_notice_count": len(notices),
                "tool_notices": notices,
                "used_tool": len(calls) > 0,
                "used_open": any(c["name"] == OPEN_TOOL for c in calls),
                "cite_flags": flags,
                "answer_not_saved": not_saved,
                "model_calls": len(usage),
                "prompt_tokens": prompt_tokens,
                "completion_tokens": completion_tokens,
                "total_tokens": prompt_tokens + completion_tokens,
                "lexical_fallback": failed > 0,
                "lexical_hit": lexical_hit(item, answer),
                "seconds": round(chat_seconds, 3),
            }
        )
        if failed > 0:
            # Only a failure on the CPU as well is an error: the recall then ran lexical.
            raise RuntimeError(f"{failed} recall(s) had no vector, not even on the CPU")
        if len(calls) != len(notices):
            row["notice_mismatch"] = True
        # The judge runs on the answer whether or not it was saved.
        judged = chat(model, judge_messages(item, answer), JUDGE_MAX_TOKENS)
        row.update(
            {
                "correct": parse_verdict(judged.text),
                "judge_reply": judged.text.strip()[:40],
                "seconds_judge": round(judged.seconds, 3),
            }
        )
    # Process, recall, HTTP and parse failures. Each becomes the row's error.
    except (
        RuntimeError,
        OSError,
        ValueError,
        TypeError,
        KeyError,
        subprocess.SubprocessError,
    ) as exc:
        row["error"] = f"{type(exc).__name__}: {exc}"[:500]
    row["seconds_total"] = round(time.monotonic() - started, 3)
    return row


def load_rows(path: Path) -> list[dict[str, object]]:
    """The rows of a results file, the last row of each conversation winning (a re-run after an
    error replaces the error row in the summary)."""
    if not path.exists():
        return []
    latest: dict[str, dict[str, object]] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        raw: object = json.loads(line)
        row = as_dict(raw, "results row")
        latest[row_key(text_of(row, "set"), text_of(row, "config"), text_of(row, "qid"))] = row
    return list(latest.values())


def share(count: int, total: int) -> str:
    return f"{100 * count / total:.0f}% ({count}/{total})" if total else "-"


def config_names(rows: list[dict[str, object]]) -> list[str]:
    known = [c.name for c in CHAT_CONFIGS]
    found = {text_of(r, "config") for r in rows}
    return [name for name in known if name in found] + sorted(found - set(known))


def summary_table(rows: list[dict[str, object]], set_name: str) -> list[str]:
    personal = set_name == "personal"
    header = (
        "| config | accuracy (judge) | correct/n | "
        + ("lexical hit, answerable | " if personal else "")
        + "errors | used a tool | mean tool calls | used open | answer not saved | "
        "cite flagged | mean total tokens | mean seconds |"
    )
    lines = [header, "|" + " --- |" * (header.count("|") - 1)]
    for name in config_names(rows):
        mine = [r for r in rows if text_of(r, "set") == set_name and text_of(r, "config") == name]
        ok = [r for r in mine if r.get("error") is None]
        scored = [r for r in ok if isinstance(r.get("correct"), bool)]
        right = sum(1 for r in scored if r.get("correct") is True)
        accuracy = f"{100 * right / len(scored):.1f}%" if scored else "-"
        cells = [name, accuracy, f"{right}/{len(scored)}"]
        if personal:
            lexical = [r for r in ok if isinstance(r.get("lexical_hit"), bool)]
            hits = sum(1 for r in lexical if r.get("lexical_hit") is True)
            cells.append(share(hits, len(lexical)))
        cells.append(str(len(mine) - len(ok)))
        cells.append(share(sum(1 for r in ok if r.get("used_tool") is True), len(ok)))
        calls = [float(int_of(r, "tool_call_count")) for r in ok]
        cells.append(mean_or_dash(calls, 2))
        cells.append(share(sum(1 for r in ok if r.get("used_open") is True), len(ok)))
        cells.append(share(sum(1 for r in ok if r.get("answer_not_saved") is True), len(ok)))
        cells.append(share(sum(1 for r in ok if flag_count(r) > 0), len(ok)))
        cells.append(mean_or_dash([float(int_of(r, "total_tokens")) for r in ok], 0))
        cells.append(mean_or_dash([num_of(r, "seconds") for r in ok], 1))
        lines.append("| " + " | ".join(cells) + " |")
    return lines


def flag_count(row: dict[str, object]) -> int:
    flags = row.get("cite_flags")
    return len(cast("list[object]", flags)) if isinstance(flags, list) else 0


def type_table(rows: list[dict[str, object]]) -> list[str]:
    header = "| config | " + " | ".join(LME_TYPE_ORDER) + " |"
    lines = [header, "|" + " --- |" * (len(LME_TYPE_ORDER) + 1)]
    for name in config_names(rows):
        cells: list[str] = []
        for qtype in LME_TYPE_ORDER:
            scored = [
                r
                for r in rows
                if text_of(r, "set") == "lme"
                and text_of(r, "config") == name
                and text_of(r, "qtype") == qtype
                and r.get("error") is None
                and isinstance(r.get("correct"), bool)
            ]
            right = sum(1 for r in scored if r.get("correct") is True)
            cells.append(f"{right}/{len(scored)}")
        lines.append(f"| {name} | " + " | ".join(cells) + " |")
    return lines


def query_table(rows: list[dict[str, object]]) -> list[str]:
    """Question vectors per configuration, summed over the conversations (all sets). A recall
    is one question vector; `lexical` counts the recalls that had none, which are error rows."""
    header = (
        "| config | gpu cache | cpu cache (fallback label) | cpu live | lexical (error) | "
        "conversations with a live cpu vector |"
    )
    lines = [header, "|" + " --- |" * (header.count("|") - 1)]
    for name in config_names(rows):
        mine = [
            r
            for r in rows
            if text_of(r, "config") == name and isinstance(r.get("query_sources"), dict)
        ]
        totals = {source: 0 for source in QUERY_SOURCES}
        live_rows = 0
        for r in mine:
            counts = cast("dict[str, object]", r["query_sources"])
            for source in QUERY_SOURCES:
                totals[source] += int_of(counts, source)
            if int_of(counts, "cpu-live") > 0:
                live_rows += 1
        cells = [
            name,
            str(totals["gpu"]),
            str(totals["cpu-cached"]),
            str(totals["cpu-live"]),
            str(totals["error"]),
            share(live_rows, len(mine)),
        ]
        lines.append("| " + " | ".join(cells) + " |")
    return lines


def summary_markdown(rows: list[dict[str, object]]) -> str:
    sections: list[str] = []
    if any(text_of(r, "set") == "lme" for r in rows):
        sections += ["## LongMemEval (lme)", *summary_table(rows, "lme")]
        sections += ["", "Per question type, correct/n:", "", *type_table(rows)]
    if any(text_of(r, "set") == "personal" for r in rows):
        sections += ["", "## Personal v2 (personal)", *summary_table(rows, "personal")]
    if not sections:
        return "no results yet"
    sections += ["", "## Question vectors (recalls, summed over conversations)", *query_table(rows)]
    sections += ["", NOTE]
    return "\n".join(sections)


def select_items(set_name: str, limit: int | None, only: set[str] | None) -> list[Item]:
    if set_name == "lme":
        return load_lme_items(limit, only)
    return load_personal_items(limit, only)


def sweep(args: argparse.Namespace) -> int:
    binary: Path = args.binary
    if not binary.exists():
        print(f"no binary at {binary}; run `make -C mojo build`", file=sys.stderr)
        return 1
    only: set[str] | None = None
    if args.qids:
        only = {part.strip() for part in str(args.qids).split(",") if part.strip()}
    configs: list[ChatConfig] = list(CHAT_CONFIGS)
    if args.configs:
        configs = [
            config_by_name(part.strip()) for part in str(args.configs).split(",") if part.strip()
        ]
    wanted_sets = ["lme", "personal"] if args.set == "both" else [str(args.set)]

    work: list[tuple[Item, ChatConfig]] = []
    for set_name in wanted_sets:
        for item in select_items(set_name, args.limit_questions, only):
            for config in configs:
                work.append((item, config))

    out: Path = args.out
    out.parent.mkdir(parents=True, exist_ok=True)
    done = completed_keys(out)
    todo = [(i, c) for i, c in work if row_key(i.set_name, c.name, i.qid) not in done]
    model = discover_model(args.model)
    print(
        f"chat eval: {len(work)} conversations, {len(done)} already done, {len(todo)} to run; "
        f"model {model}; results {out}",
        file=sys.stderr,
        flush=True,
    )

    live_cache: Path = args.live_cache
    if live_cache.resolve() == GPU_CACHE.resolve():
        raise ValueError("--live-cache must not be the GPU cache")
    live_cache.parent.mkdir(parents=True, exist_ok=True)
    seconds_run: list[float] = []
    failures = 0
    with tempfile.TemporaryDirectory(prefix="vinci-chat-eval-") as raw_root:
        root = Path(raw_root)
        with out.open("a", encoding="utf-8") as handle:
            for index, (item, config) in enumerate(todo, start=1):
                row = run_conversation(
                    binary, model, item, config, root, live_cache, args.transcripts
                )
                handle.write(json.dumps(row, ensure_ascii=False) + "\n")
                handle.flush()
                seconds_run.append(float(cast("float", row["seconds_total"])))
                if row["error"] is not None:
                    failures += 1
                eta = statistics.fmean(seconds_run) * (len(todo) - index)
                status = (
                    f"ERROR {row['error']}"
                    if row["error"] is not None
                    else f"correct={'yes' if row.get('correct') else 'no'}"
                    f" tools={row.get('tool_call_count')} tokens={row.get('total_tokens')}"
                    f" chat={row.get('seconds')}s"
                )
                print(
                    f"[{index}/{len(todo)} ETA {format_seconds(eta)}] {item.set_name}"
                    f" {config.name} {item.qid}: {status}",
                    file=sys.stderr,
                    flush=True,
                )
    print(
        f"done: {len(todo)} conversations this run, {failures} errors, "
        f"{sum(seconds_run):.0f} s wall in the loop",
        file=sys.stderr,
        flush=True,
    )
    print(summary_markdown(load_rows(out)))
    return 0


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="python -m vinci_eval.chat_eval")
    parser.add_argument("--set", choices=("lme", "personal", "both"), default="both")
    parser.add_argument("--configs", help="comma-separated: chat-w10, chat-w2 (default: both)")
    parser.add_argument("--limit-questions", type=int, help="the first N questions of each set")
    parser.add_argument("--qids", help="comma-separated question ids to run (testing)")
    default_out = RESULTS_DIR / f"chat-eval-{datetime.date.today().isoformat()}.jsonl"
    parser.add_argument("--out", type=Path, default=default_out, help="results JSON lines")
    parser.add_argument(
        "--transcripts", type=Path, help="directory for each conversation's raw stdout (smoke runs)"
    )
    parser.add_argument(
        "--live-cache",
        type=Path,
        default=LIVE_CACHE_DEFAULT,
        help="scratch cache for live CPU question vectors (never the GPU cache)",
    )
    parser.add_argument("--summary", action="store_true", help="print the summary and exit")
    parser.add_argument("--binary", type=Path, default=DEFAULT_BINARY)
    parser.add_argument("--model", help="chat model id (default: the first from /v1/models)")
    args = parser.parse_args(argv)
    if args.summary:
        print(summary_markdown(load_rows(args.out)))
        return 0
    return sweep(args)


if __name__ == "__main__":
    raise SystemExit(main())
