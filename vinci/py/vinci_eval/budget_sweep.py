"""Budget sweep for answers (M3 step 3b): does budgeted recall keep answer accuracy?

For each question, the recall context of one configuration is packed by the binary and sent to
the chat model with the question. The reply is judged by the same model. One JSON line per
answer goes to a resumable results file.

Configurations (names as they appear in the results and the summary):

- baseline: the unbudgeted top 10, every record whole (`--budget 10000000 --whole 10`, the
  "10 whole" packed form that index_eval.py scores). Tokens: ceil(bytes / 4) of that text.
- b<N>w<W>: `vinci recall --budget N --whole W`. The packer fills the best W records whole, then
  the rest as pointers, stopping at the budget. Tokens: the binary's packed_tokens, which must
  equal ceil(bytes / 4) of the packed text and must not exceed N.
- b<N>: the same as b<N>w2 (the binary's default of two whole records). A plain number is the
  same again.

Question sets:

- lme: the fixed 100 LongMemEval questions (mojo/eval/longmemeval-subset.json). Each question
  has its own index in ~/.vinci/eval/lme-index/, built by lme_index_eval.py with cached
  embeddings. Recall uses the same label, cache and corrective-query fallback as
  lme_index_eval's scored runs, with closed base URLs. A recall that needs an uncached embedding
  fails and is recorded as an error row. No embedding server is called.
- personal: the 60 personal v2 questions (index_eval.py). They run on a temporary copy of
  ~/.vinci/staging (journal, index and docs). The staging home is only read. The same
  closed-URL rule applies. Each answerable row also gets a lexical check: whether every
  expectedContains string is a case-insensitive substring of the answer.

The answer model is the llama-server chat model on 127.0.0.1:8080, at temperature 0 with
thinking disabled. The judge is that same model (self-judging bias: the judge is the answering
model). Its prompts are the LongMemEval evaluate_qa prompts by question type, with the
abstention prompt for ids ending in _abs. The vendored LongMemEval repository is not on this
machine, so these are reconstructed from the published evaluation and are not verbatim. For
personal questions, the "correct answer" is the list of expectedContains strings, which the
response must state in any wording.

Usage, from mojo/py:
    uv run python -m vinci_eval.budget_sweep --limit-questions 3 --out SCRATCH.jsonl
    uv run python -m vinci_eval.budget_sweep --set both
    uv run python -m vinci_eval.budget_sweep --configs baseline,b250w10 --set personal
    uv run python -m vinci_eval.budget_sweep --summary --out RESULTS.jsonl
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import re
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
import urllib.request
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Final, cast

from vinci_edge.embed import DEFAULT_CACHE

from .index_eval import DEFAULT_BINARY, DEFAULT_HOME, DEFAULT_LABEL, QUESTIONS, estimate_tokens
from .lme_index_eval import CLOSED_URL, CPU_LABEL, GPU_CACHE, INDEX_DIR, LABEL, iso_of
from .lme_subset import (
    DATASET_SHA256,
    DEFAULT_DATASET,
    DEFAULT_SUBSET,
    load_subset_ids,
    sha256_file,
)
from .personal_baseline import RESULTS_DIR, Question, load_questions

CHAT_URL: Final = "http://127.0.0.1:8080"
CHAT_TIMEOUT_S: Final = 900
ANSWER_MAX_TOKENS: Final = 256
JUDGE_MAX_TOKENS: Final = 32
TOP_K: Final = 10
UNLIMITED: Final = 10_000_000
ROUTES: Final = "lexical,vector"
BASELINE: Final = "baseline"
DEFAULT_WHOLE: Final = 2
LME_CONFIG_NAMES: Final[tuple[str, ...]] = (
    BASELINE,
    "b500w2",
    "b1000w2",
    "b1000w10",
    "b2000w10",
    "b3000w10",
    "b5000w10",
)
PERSONAL_CONFIG_NAMES: Final[tuple[str, ...]] = (
    BASELINE,
    "b100w2",
    "b150w2",
    "b250w2",
    "b250w10",
    "b500w10",
)
CONFIG_NAME: Final = re.compile(r"b(\d+)(?:w(\d+))?")
PLAIN_BUDGET: Final = re.compile(r"\d+")
SET_NAMES: Final = ("lme", "personal")
ABS_SUFFIX: Final = "_abs"
ABSTENTION: Final = "abstention"
LME_TYPE_ORDER: Final = (
    "temporal-reasoning",
    "multi-session",
    "knowledge-update",
    "single-session-user",
    "single-session-assistant",
    "single-session-preference",
    ABSTENTION,
)
THINK: Final = re.compile(r"<think>.*?</think>", re.DOTALL)
YES_NO: Final = re.compile(r"^\W*(yes|no)\b", re.IGNORECASE)

SYSTEM_PROMPT: Final = (
    "You answer questions about a person's memory. Answer only from the memory excerpts in "
    "the user's message. Each excerpt starts with an id in brackets. If the excerpts do not "
    "contain the answer, say that you don't know. Be brief."
)

# The LongMemEval evaluate_qa prompts, by question type. Reconstructed, not verbatim.
DEFAULT_JUDGE: Final = (
    "I will give you a question, a correct answer, and a response from a model. Please answer "
    "yes or no. If the response contains the correct answer, say yes. If the response does not "
    "contain the correct answer, say no."
)
TEMPORAL_JUDGE: Final = (
    DEFAULT_JUDGE
    + " If the response is equivalent to the correct answer or contains all the intermediate "
    "steps to get the correct answer, say yes. If the response only contains a subset of the "
    "information required by the answer, answer no. In addition, do not penalize off-by-one "
    "errors for the number of days. If the question asks for the number of days, weeks, months, "
    "etc., and the model makes off-by-one errors (e.g., predicting 19 days when the answer is "
    "18), the model's response is still correct."
)
KNOWLEDGE_JUDGE: Final = (
    DEFAULT_JUDGE
    + " If the response contains some previous information along with an updated answer, the "
    "response should be considered as correct as long as the updated answer is the required "
    "answer."
)
PREFERENCE_JUDGE: Final = (
    "I will give you a question, a rubric for desired personalized response, and a response "
    "from a model. Please answer yes or no based on whether the response satisfies the rubric. "
    "If the model response addresses the desired personalized response, say yes. Otherwise, say "
    "no."
)
ABSTENTION_JUDGE: Final = (
    "I will give you an unanswerable question, an explanation, and a response from a model. "
    "Please answer yes or no. If the model says the question is unanswerable or that it does not "
    "know, say yes. If the model answers the question, even partially, say no."
)
JUDGE_BY_TYPE: Final[dict[str, str]] = {
    "temporal-reasoning": TEMPORAL_JUDGE,
    "knowledge-update": KNOWLEDGE_JUDGE,
    "single-session-preference": PREFERENCE_JUDGE,
}


@dataclass(frozen=True)
class Config:
    """One recall configuration. budget None is the baseline (the unbudgeted top 10, all whole)."""

    budget: int | None
    whole: int

    @property
    def name(self) -> str:
        return BASELINE if self.budget is None else f"b{self.budget}w{self.whole}"


@dataclass(frozen=True)
class Item:
    """One question, as the answer and the judge see it."""

    set_name: str
    qid: str
    qtype: str
    abstention: bool
    question: str
    # The reference for the judge: the gold answer (LongMemEval), the rubric, or the facts a
    # personal answer must state.
    expected: str
    # Personal answerable questions only: the expectedContains strings for the lexical check.
    terms: tuple[str, ...]
    # LongMemEval only: the question's date, as the answer prompt shows it, and as VINCI_NOW.
    date_text: str | None
    now_iso: str | None


@dataclass(frozen=True)
class Reply:
    text: str
    prompt_tokens: int
    completion_tokens: int
    finish: str
    seconds: float


@dataclass(frozen=True)
class Result:
    """One row of the results file, read back for the summary."""

    set_name: str
    config: str
    qtype: str
    correct: bool | None
    lexical_hit: bool | None
    tokens: int | None
    whole: int | None
    pointers: int | None
    omitted: int | None
    seconds: float | None
    answer_seconds: float | None
    judge_seconds: float | None
    error: str | None


def as_dict(value: object, what: str) -> dict[str, object]:
    if not isinstance(value, dict):
        raise TypeError(f"{what} must be a JSON object")
    return cast("dict[str, object]", value)


def as_list(value: object, what: str) -> list[object]:
    if not isinstance(value, list):
        raise TypeError(f"{what} must be a JSON list")
    return cast("list[object]", value)


def int_or_zero(value: object) -> int:
    return value if isinstance(value, int) and not isinstance(value, bool) else 0


def parse_config(text: str) -> Config:
    """`baseline`, `b<N>w<W>`, `b<N>` (whole 2) or a plain number N (the same as `b<N>`)."""
    text = text.strip()
    if text == BASELINE:
        return Config(None, TOP_K)
    if PLAIN_BUDGET.fullmatch(text):
        budget, whole = int(text), DEFAULT_WHOLE
    else:
        match = CONFIG_NAME.fullmatch(text)
        if match is None:
            raise ValueError(f"not a configuration: {text!r} (use baseline or b<N>w<W>)")
        budget = int(match.group(1))
        whole = int(match.group(2)) if match.group(2) is not None else DEFAULT_WHOLE
    if budget < 1:
        raise ValueError(f"budget must be at least 1: {text!r}")
    if not 0 <= whole <= TOP_K:
        raise ValueError(f"whole must be between 0 and {TOP_K}: {text!r}")
    return Config(budget, whole)


def config_order(name: str) -> tuple[int, int]:
    if name == BASELINE:
        return (-1, 0)
    match = CONFIG_NAME.fullmatch(name)
    if match is None:
        return (1 << 30, 0)
    return (int(match.group(1)), int(match.group(2) or DEFAULT_WHOLE))


def strip_thinking(text: str) -> str:
    """Drop a <think> block, and anything before a stray closing tag."""
    text = THINK.sub("", text)
    if "</think>" in text:
        text = text.rsplit("</think>", 1)[1]
    return text.strip()


def get_json(url: str) -> dict[str, object]:
    with urllib.request.urlopen(url, timeout=30) as handle:
        raw: object = json.loads(handle.read().decode("utf-8"))
    return as_dict(raw, url)


def discover_model(override: str | None) -> str:
    if override:
        return override
    data = as_list(get_json(f"{CHAT_URL}/v1/models").get("data"), "models data")
    if not data:
        raise ValueError("the chat server lists no models")
    return str(as_dict(data[0], "model").get("id"))


def chat(model: str, messages: list[dict[str, str]], max_tokens: int) -> Reply:
    """One chat completion at temperature 0 with thinking off."""
    body: dict[str, object] = {
        "model": model,
        "messages": messages,
        "temperature": 0,
        "max_tokens": max_tokens,
        "chat_template_kwargs": {"enable_thinking": False},
    }
    request = urllib.request.Request(
        f"{CHAT_URL}/v1/chat/completions",
        data=json.dumps(body).encode("utf-8"),
        headers={"Content-Type": "application/json"},
    )
    started = time.monotonic()
    with urllib.request.urlopen(request, timeout=CHAT_TIMEOUT_S) as handle:
        raw: object = json.loads(handle.read().decode("utf-8"))
    seconds = time.monotonic() - started
    reply = as_dict(raw, "chat reply")
    choices = as_list(reply.get("choices"), "choices")
    if not choices:
        raise ValueError("chat reply has no choices")
    first = as_dict(choices[0], "choice")
    message = as_dict(first.get("message"), "message")
    content = message.get("content")
    if not isinstance(content, str):
        raise TypeError("chat reply content must be a string")
    usage_raw = reply.get("usage")
    usage: dict[str, object] = (
        cast("dict[str, object]", usage_raw) if isinstance(usage_raw, dict) else {}
    )
    finish = first.get("finish_reason")
    return Reply(
        text=content,
        prompt_tokens=int_or_zero(usage.get("prompt_tokens")),
        completion_tokens=int_or_zero(usage.get("completion_tokens")),
        finish=str(finish),
        seconds=seconds,
    )


def answer_messages(item: Item, packed: str) -> list[dict[str, str]]:
    body = packed if packed else "(no excerpts)"
    parts = [f"Memory excerpts:\n{body}"]
    if item.date_text is not None:
        parts.append(f"Current date: {item.date_text}")
    parts.append(f"Question: {item.question}")
    return [
        {"role": "system", "content": SYSTEM_PROMPT},
        {"role": "user", "content": "\n\n".join(parts)},
    ]


def judge_messages(item: Item, response: str) -> list[dict[str, str]]:
    if item.abstention:
        instructions = ABSTENTION_JUDGE
        label = "Explanation"
        final = (
            "Does the model correctly identify the question as unanswerable? Answer yes or no only."
        )
    else:
        instructions = JUDGE_BY_TYPE.get(item.qtype, DEFAULT_JUDGE)
        label = "Rubric" if item.qtype == "single-session-preference" else "Correct Answer"
        final = "Is the model response correct? Answer yes or no only."
    content = (
        f"{instructions}\n\nQuestion: {item.question}\n\n{label}: {item.expected}"
        f"\n\nModel Response: {response}\n\n{final}"
    )
    return [{"role": "user", "content": content}]


def parse_verdict(reply: str) -> bool:
    text = strip_thinking(reply)
    match = YES_NO.match(text)
    if match is None:
        raise ValueError(f"judge reply is not yes or no: {text[:60]!r}")
    return match.group(1).lower() == "yes"


def lexical_hit(item: Item, answer: str) -> bool | None:
    """Every expectedContains string is a case-insensitive substring of the answer. None for
    LongMemEval and for abstention questions, which have no strings to find."""
    if item.set_name != "personal" or item.abstention:
        return None
    lowered = answer.lower()
    return all(term.lower() in lowered for term in item.terms)


def load_lme_items(limit: int | None, only: set[str] | None) -> list[Item]:
    digest = sha256_file(DEFAULT_DATASET)
    if digest != DATASET_SHA256:
        raise ValueError(f"dataset sha256 {digest} does not match {DATASET_SHA256}")
    raw = as_list(json.loads(DEFAULT_DATASET.read_text(encoding="utf-8")), "dataset")
    by_id: dict[str, dict[str, object]] = {}
    for entry in raw:
        record = as_dict(entry, "dataset entry")
        by_id[str(record["question_id"])] = record
    del raw
    items: list[Item] = []
    for qid in load_subset_ids(DEFAULT_SUBSET):
        if only is not None and qid not in only:
            continue
        record = by_id[qid]
        date_text = str(record["question_date"])
        abstention = qid.endswith(ABS_SUFFIX)
        items.append(
            Item(
                set_name="lme",
                qid=qid,
                qtype=ABSTENTION if abstention else str(record["question_type"]),
                abstention=abstention,
                question=str(record["question"]),
                expected=str(record["answer"]),
                terms=(),
                date_text=date_text,
                now_iso=iso_of(date_text),
            )
        )
    return items[:limit] if limit is not None else items


def load_personal_items(limit: int | None, only: set[str] | None) -> list[Item]:
    items: list[Item] = []
    question: Question
    for question in load_questions(QUESTIONS):
        if only is not None and question.qid not in only:
            continue
        if question.should_abstain:
            expected = (
                "The journal holds no record that answers this question, so a correct response "
                "says that it does not know."
            )
        else:
            expected = (
                "A correct response states each of these facts, in any wording: "
                + "; ".join(question.expected)
            )
        items.append(
            Item(
                set_name="personal",
                qid=question.qid,
                qtype=question.qtype,
                abstention=question.should_abstain,
                question=question.query,
                expected=expected,
                terms=question.expected,
                date_text=None,
                now_iso=None,
            )
        )
    return items[:limit] if limit is not None else items


def copy_staging(dest: Path) -> None:
    """A copy of the staging journal, index and docs. The staging home is only read."""
    if not (DEFAULT_HOME / "journal.jsonl").exists():
        raise FileNotFoundError(f"no journal at {DEFAULT_HOME / 'journal.jsonl'}")
    dest.mkdir(parents=True)
    shutil.copy2(DEFAULT_HOME / "journal.jsonl", dest / "journal.jsonl")
    for name in sorted(path.name for path in DEFAULT_HOME.glob("index.sqlite*")):
        shutil.copy2(DEFAULT_HOME / name, dest / name)
    if (DEFAULT_HOME / "docs").is_dir():
        shutil.copytree(DEFAULT_HOME / "docs", dest / "docs")


def recall(
    binary: Path,
    item: Item,
    home: Path,
    env_file: Path,
    budget: int,
    whole: int,
) -> dict[str, object]:
    """One `vinci recall --json` call. Embeddings come from the cache only: the base URL is
    closed, and a miss fails the call."""
    env = dict(os.environ)
    env.pop("VINCI_NOW", None)
    env.update(
        {
            "VINCI_HOME": str(home),
            "VINCI_ENV_FILE": str(env_file),
            "VINCI_NO_TRACK": "1",
            "EMBEDDING_BASE_URL": CLOSED_URL,
        }
    )
    if item.now_iso is not None:
        env["VINCI_NOW"] = item.now_iso
    args = ["recall", "--json", "--limit", str(TOP_K), "--routes", ROUTES]
    if item.set_name == "lme":
        args += [
            "--index",
            str(INDEX_DIR / f"{item.qid}.sqlite"),
            "--label",
            LABEL,
            "--base-url",
            CLOSED_URL,
            "--cache",
            str(GPU_CACHE),
            "--query-fallback-label",
            CPU_LABEL,
            "--query-fallback-url",
            CLOSED_URL,
        ]
    else:
        args += [
            "--label",
            DEFAULT_LABEL,
            "--base-url",
            CLOSED_URL,
            "--cache",
            str(DEFAULT_CACHE),
        ]
    args += ["--budget", str(budget), "--whole", str(whole), item.question]
    completed = subprocess.run(
        [str(binary), *args], env=env, capture_output=True, text=True, check=False
    )
    if completed.returncode != 0:
        tail = completed.stderr.strip()[-300:]
        raise RuntimeError(f"recall exited {completed.returncode}: {tail}")
    raw: object = json.loads(completed.stdout)
    return as_dict(raw, "recall output")


def run_one(
    binary: Path,
    model: str,
    item: Item,
    config: Config,
    home: Path,
    env_file: Path,
) -> dict[str, object]:
    """Recall, answer and judge one question under one configuration. Failures are kept as
    an `error` field on the row, never raised, so the sweep goes on."""
    row: dict[str, object] = {
        "set": item.set_name,
        "config": config.name,
        "qid": item.qid,
        "qtype": item.qtype,
        "abstention": item.abstention,
        "model": model,
        "run_at": datetime.datetime.now(datetime.UTC).isoformat(timespec="seconds"),
        "error": None,
    }
    started = time.monotonic()
    try:
        if config.budget is None:
            response = recall(binary, item, home, env_file, UNLIMITED, TOP_K)
        else:
            response = recall(binary, item, home, env_file, config.budget, config.whole)
        packed_raw = response.get("packed")
        if not isinstance(packed_raw, str):
            raise TypeError("recall output: packed must be a string")
        packed = packed_raw
        counted = estimate_tokens(packed)
        if config.budget is None:
            tokens = counted
        else:
            tokens = int_or_zero(response.get("packed_tokens"))
            if tokens != counted:
                raise ValueError(f"binary counted {tokens} tokens, the estimator {counted}")
            if tokens > config.budget:
                raise ValueError(f"packed text is {tokens} tokens, over budget {config.budget}")
        row.update(
            {
                "context_tokens": tokens,
                "packed_whole": int_or_zero(response.get("packed_whole")),
                "packed_pointers": int_or_zero(response.get("packed_pointers")),
                "packed_omitted": int_or_zero(response.get("packed_omitted")),
                "recall_abstain": response.get("abstain") is True,
                "seconds_recall": round(time.monotonic() - started, 3),
            }
        )
        answer = chat(model, answer_messages(item, packed), ANSWER_MAX_TOKENS)
        response_text = strip_thinking(answer.text)
        row.update(
            {
                "answer": response_text,
                "answer_finish": answer.finish,
                "answer_prompt_tokens": answer.prompt_tokens,
                "answer_completion_tokens": answer.completion_tokens,
                "seconds_answer": round(answer.seconds, 3),
                "lexical_hit": lexical_hit(item, response_text),
            }
        )
        judged = chat(model, judge_messages(item, response_text), JUDGE_MAX_TOKENS)
        verdict = parse_verdict(judged.text)
        row.update(
            {
                "judge_reply": strip_thinking(judged.text)[:40],
                "correct": verdict,
                "judge_prompt_tokens": judged.prompt_tokens,
                "judge_completion_tokens": judged.completion_tokens,
                "seconds_judge": round(judged.seconds, 3),
            }
        )
    # Recall, HTTP and parse failures (json and urllib errors are OSError or ValueError).
    except (RuntimeError, OSError, ValueError, TypeError, KeyError) as exc:
        row["error"] = f"{type(exc).__name__}: {exc}"[:500]
    row["seconds"] = round(time.monotonic() - started, 3)
    return row


def row_key(set_name: str, config: Config, qid: str) -> str:
    return f"{set_name}|{config.name}|{qid}"


def completed_keys(path: Path) -> set[str]:
    """Keys of rows that finished without an error. Rows with an error run again."""
    if not path.exists():
        return set()
    done: set[str] = set()
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        row = as_dict(json.loads(line), "results row")
        if row.get("error") is None:
            done.add(f"{row['set']}|{row['config']}|{row['qid']}")
    return done


def format_seconds(seconds: float) -> str:
    if seconds >= 3600:
        return f"{seconds / 3600:.1f} h"
    if seconds >= 60:
        return f"{seconds / 60:.1f} min"
    return f"{seconds:.0f} s"


def sweep(args: argparse.Namespace) -> int:
    binary: Path = args.binary
    if not binary.exists():
        print(f"no binary at {binary}; run `make -C mojo build`", file=sys.stderr)
        return 1
    only: set[str] | None = None
    if args.qids:
        only = {part.strip() for part in str(args.qids).split(",") if part.strip()}
    wanted_sets = list(SET_NAMES) if args.set == "both" else [str(args.set)]
    override: list[Config] | None = None
    if args.configs:
        override = [parse_config(part) for part in str(args.configs).split(",") if part.strip()]

    work: list[tuple[Item, Config]] = []
    for set_name in wanted_sets:
        if set_name == "lme":
            items = load_lme_items(args.limit_questions, only)
            names = LME_CONFIG_NAMES
        else:
            items = load_personal_items(args.limit_questions, only)
            names = PERSONAL_CONFIG_NAMES
        configs = override or [parse_config(name) for name in names]
        for item in items:
            for config in configs:
                work.append((item, config))

    out: Path = args.out
    out.parent.mkdir(parents=True, exist_ok=True)
    done = completed_keys(out)
    todo = [(i, c) for i, c in work if row_key(i.set_name, c, i.qid) not in done]
    model = discover_model(args.model)
    print(
        f"budget sweep: {len(work)} answers in {len(wanted_sets)} set(s), {len(done)} already "
        f"done, {len(todo)} to run; model {model}; results {out}",
        file=sys.stderr,
        flush=True,
    )

    seconds_run: list[float] = []
    failures = 0
    with tempfile.TemporaryDirectory(prefix="vinci-budget-") as tmp:
        root = Path(tmp)
        env_file = root / "empty.env"
        env_file.write_text("", encoding="utf-8")
        lme_home = root / "lme-home"
        lme_home.mkdir()
        personal_home = root / "personal-home"
        if "personal" in wanted_sets:
            copy_staging(personal_home)
        with out.open("a", encoding="utf-8") as handle:
            for index, (item, config) in enumerate(todo, start=1):
                home = lme_home if item.set_name == "lme" else personal_home
                row = run_one(binary, model, item, config, home, env_file)
                handle.write(json.dumps(row, ensure_ascii=False) + "\n")
                handle.flush()
                seconds_run.append(float(cast("float", row["seconds"])))
                if row["error"] is not None:
                    failures += 1
                remaining = len(todo) - index
                eta = statistics.fmean(seconds_run) * remaining
                status = (
                    f"ERROR {row['error']}"
                    if row["error"] is not None
                    else f"correct={'yes' if row.get('correct') else 'no'}"
                    f" ctx={row.get('context_tokens')}"
                    f" answer={row.get('seconds_answer')}s judge={row.get('seconds_judge')}s"
                )
                print(
                    f"[{index}/{len(todo)} ETA {format_seconds(eta)}] {item.set_name}"
                    f" {config.name} {item.qid}: {status}",
                    file=sys.stderr,
                    flush=True,
                )
    print(
        f"done: {len(todo)} answers this run, {failures} errors, "
        f"{sum(seconds_run):.0f} s wall in the loop",
        file=sys.stderr,
        flush=True,
    )
    print(summary_markdown(load_results(out)))
    return 0


def optional_int(row: dict[str, object], key: str) -> int | None:
    value = row.get(key)
    return value if isinstance(value, int) and not isinstance(value, bool) else None


def load_results(path: Path) -> list[Result]:
    if not path.exists():
        return []
    results: list[Result] = []
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        row = as_dict(json.loads(line), "results row")
        results.append(
            Result(
                set_name=str(row["set"]),
                config=str(row["config"]),
                qtype=str(row["qtype"]),
                correct=cast("bool | None", row.get("correct")),
                lexical_hit=cast("bool | None", row.get("lexical_hit")),
                tokens=optional_int(row, "context_tokens"),
                whole=optional_int(row, "packed_whole"),
                pointers=optional_int(row, "packed_pointers"),
                omitted=optional_int(row, "packed_omitted"),
                seconds=cast("float | None", row.get("seconds")),
                answer_seconds=cast("float | None", row.get("seconds_answer")),
                judge_seconds=cast("float | None", row.get("seconds_judge")),
                error=cast("str | None", row.get("error")),
            )
        )
    return results


def mean_or_dash(values: list[float], digits: int) -> str:
    return f"{statistics.fmean(values):.{digits}f}" if values else "-"


def summary_table(results: list[Result], set_name: str) -> list[str]:
    """One row per configuration. The lexical column is for the personal set only."""
    configs = sorted({r.config for r in results}, key=config_order)
    personal = set_name == "personal"
    header = (
        "| config | accuracy (judge) | correct/n | "
        + ("lexical hit, answerable | " if personal else "")
        + "errors | mean ctx tokens | median | mean whole | mean pointers | mean omitted | "
        "mean seconds | mean answer s | mean judge s |"
    )
    divider = "|" + " --- |" * (header.count("|") - 1)
    lines = [header, divider]
    for config in configs:
        rows = [r for r in results if r.config == config and r.set_name == set_name]
        scored = [r for r in rows if r.error is None and r.correct is not None]
        right = sum(1 for r in scored if r.correct)
        accuracy = f"{100 * right / len(scored):.1f}%" if scored else "-"
        ok = [r for r in rows if r.error is None]
        tokens = [float(r.tokens) for r in rows if r.tokens is not None]
        median = f"{statistics.median(tokens):.0f}" if tokens else "-"
        cells = [config, accuracy, f"{right}/{len(scored)}"]
        if personal:
            lexical = [r for r in ok if r.lexical_hit is not None]
            lex_right = sum(1 for r in lexical if r.lexical_hit)
            cells.append(
                f"{100 * lex_right / len(lexical):.1f}% ({lex_right}/{len(lexical)})"
                if lexical
                else "-"
            )
        cells.append(str(sum(1 for r in rows if r.error is not None)))
        cells.append(mean_or_dash(tokens, 0))
        cells.append(median)
        for field in (
            [float(r.whole) for r in ok if r.whole is not None],
            [float(r.pointers) for r in ok if r.pointers is not None],
            [float(r.omitted) for r in ok if r.omitted is not None],
        ):
            cells.append(mean_or_dash(field, 2))
        seconds = [r.seconds for r in ok if r.seconds is not None]
        answer = [r.answer_seconds for r in ok if r.answer_seconds is not None]
        judge = [r.judge_seconds for r in ok if r.judge_seconds is not None]
        cells += [mean_or_dash(seconds, 2), mean_or_dash(answer, 2), mean_or_dash(judge, 2)]
        lines.append("| " + " | ".join(cells) + " |")
    return lines


def type_table(results: list[Result]) -> list[str]:
    configs = sorted({r.config for r in results}, key=config_order)
    header = "| config | " + " | ".join(LME_TYPE_ORDER) + " |"
    lines = [header, "|" + " --- |" * (len(LME_TYPE_ORDER) + 1)]
    for config in configs:
        cells: list[str] = []
        for qtype in LME_TYPE_ORDER:
            rows = [
                r
                for r in results
                if r.config == config
                and r.set_name == "lme"
                and r.qtype == qtype
                and r.error is None
                and r.correct is not None
            ]
            right = sum(1 for r in rows if r.correct)
            cells.append(f"{right}/{len(rows)}")
        lines.append(f"| {config} | " + " | ".join(cells) + " |")
    return lines


def summary_markdown(results: list[Result]) -> str:
    """The summary: one table per set, and a per-type table for LongMemEval."""
    sections: list[str] = []
    if any(r.set_name == "lme" for r in results):
        sections.append("## LongMemEval (lme)")
        sections += summary_table(results, "lme")
        sections += ["", "Per question type, correct/n:", ""]
        sections += type_table(results)
    if any(r.set_name == "personal" for r in results):
        sections += ["", "## Personal v2 (personal)"]
        sections += summary_table(results, "personal")
    if not sections:
        return "no results yet"
    sections += [
        "",
        (
            "Judge: the answering model itself (self-judging bias). Accuracy is the judge's "
            "verdict over judged answers; the lexical column is every expectedContains string "
            "found in the answer, over answerable questions only. Means cover rows without an "
            "error. Whole, pointer and omitted counts are the binary's packed_whole, "
            "packed_pointers and packed_omitted."
        ),
    ]
    return "\n".join(sections)


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="python -m vinci_eval.budget_sweep")
    parser.add_argument("--set", choices=("lme", "personal", "both"), default="both")
    parser.add_argument(
        "--configs",
        help="comma-separated: baseline, b<N>w<W>, b<N> or N (default: each set's own list)",
    )
    parser.add_argument("--limit-questions", type=int, help="the first N questions of each set")
    parser.add_argument("--qids", help="comma-separated question ids to run (testing)")
    default_out = RESULTS_DIR / f"budget-sweep-{datetime.date.today().isoformat()}.jsonl"
    parser.add_argument("--out", type=Path, default=default_out, help="results JSON lines")
    parser.add_argument("--summary", action="store_true", help="print the summary and exit")
    parser.add_argument("--binary", type=Path, default=DEFAULT_BINARY)
    parser.add_argument("--model", help="chat model id (default: the first from /v1/models)")
    args = parser.parse_args(argv)
    if args.summary:
        print(summary_markdown(load_results(args.out)))
        return 0
    return sweep(args)


if __name__ == "__main__":
    raise SystemExit(main())
