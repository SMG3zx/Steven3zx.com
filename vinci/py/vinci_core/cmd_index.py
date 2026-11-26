"""The index and recall commands: `vinci index rebuild|update|stats|load|embed`,
`vinci recall [--json] [--limit N] [--routes lexical,vector] [--index PATH] [--label L]
[--base-url U] [--cache PATH] [--budget N [--whole N] [--tokenizer-url U]] <query...>`, and
`vinci open <id> [lines=A-B] [grep=word] [--index PATH] [--json]`. None of them changes the
journal. Index files live beside it, or at --index PATH.

Port of mojo/src/cmd_index.mojo.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional

from .dates import clock_now, today_of
from .embed_config import EmbedConfig, default_config
from .fusion import Candidate, Weights, parse_weights
from .handles import lookup
from .index_build import BuildReport, load_records, update_from_journal
from .index_store import IndexStore
from .jsonline import format_json_double, json_string
from .open_view import Request, build_view, parse_lines_arg, render_json, render_text
from .pack import DEFAULT_WHOLE, METHOD_SERVER, Counter, Packed, estimate_counter, pack
from .recall import DEFAULT_ROUTES, Recall, Settings, feed_hint_line, parse_sources, recall
from .session import user_error
from .text import clip
from .timeutil import format_instant  # noqa: F401  (kept for parity with the Mojo imports)
from .vectors import embed_missing

INDEX_FILE = "index.sqlite"
DEFAULT_LIMIT = 10
OPEN_CAP_BYTES = 4000

VALUE_FLAGS = (
    "--index", "--limit", "--routes", "--label", "--base-url", "--cache", "--model",
    "--query-fallback-label", "--query-fallback-url", "--budget", "--whole",
    "--tokenizer-url", "--sources", "--weights",
)


@dataclass
class Options:
    index_path: Optional[str] = None
    limit: int = DEFAULT_LIMIT
    routes: list[str] = field(default_factory=list)
    json: bool = False
    rest: list[str] = field(default_factory=list)
    label: Optional[str] = None
    base_url: Optional[str] = None
    cache: Optional[str] = None
    model: Optional[str] = None
    fallback_label: Optional[str] = None
    fallback_url: Optional[str] = None
    budget: Optional[int] = None
    whole: int = DEFAULT_WHOLE
    tokenizer_url: Optional[str] = None
    sources: list[str] = field(default_factory=list)
    weights: Optional[Weights] = None


def join_path(directory: str, name: str) -> str:
    if directory.endswith("/"):
        return directory + name
    return directory + "/" + name


def parse_options(args: list[str]) -> Options:
    """Flags may come anywhere. Everything else is a positional argument."""
    opts = Options()
    i = 0
    while i < len(args):
        a = args[i]
        if a == "--json":
            opts.json = True
        elif a in VALUE_FLAGS:
            if i + 1 >= len(args):
                raise user_error(a + " needs a value")
            value = args[i + 1]
            i += 1
            if a == "--index":
                opts.index_path = value
            elif a == "--label":
                opts.label = value
            elif a == "--base-url":
                opts.base_url = value
            elif a == "--cache":
                opts.cache = value
            elif a == "--model":
                opts.model = value
            elif a == "--query-fallback-label":
                opts.fallback_label = value
            elif a == "--query-fallback-url":
                opts.fallback_url = value
            elif a == "--tokenizer-url":
                opts.tokenizer_url = value
            elif a == "--sources":
                opts.sources = parse_sources(value)
            elif a == "--weights":
                opts.weights = parse_weights(value)
            elif a == "--budget":
                try:
                    opts.budget = int(value)
                except ValueError:
                    raise user_error("--budget must be a whole number") from None
                if opts.budget < 1:
                    raise user_error("--budget must be at least 1")
            elif a == "--whole":
                try:
                    opts.whole = int(value)
                except ValueError:
                    raise user_error("--whole must be a whole number") from None
                if opts.whole < 0:
                    raise user_error("--whole must be 0 or more")
            elif a == "--limit":
                try:
                    opts.limit = int(value)
                except ValueError:
                    raise user_error("--limit must be a whole number") from None
                if opts.limit <= 0:
                    raise user_error("--limit must be at least 1")
            else:
                opts.routes = value.split(",")
        else:
            opts.rest.append(a)
        i += 1
    if len(opts.routes) == 0:
        opts.routes = DEFAULT_ROUTES.split(",")
    if len(opts.sources) == 0:
        opts.sources = ["journal"]
    return opts


def embed_config(opts: Options) -> EmbedConfig:
    """The embedding settings: the environment's, with any flag replacing one."""
    cfg = default_config()
    if opts.model is not None:
        cfg.model = opts.model
        cfg.label = opts.model
    if opts.label is not None:
        cfg.label = opts.label
    if opts.base_url is not None:
        cfg.base_url = opts.base_url
    if opts.cache is not None:
        cfg.cache = opts.cache
    if opts.fallback_url is not None:
        cfg.fallback_label = opts.fallback_label or ""
        cfg.fallback_url = opts.fallback_url
    return cfg


def index_path_of(opts: Options, home: str) -> str:
    if opts.index_path is not None:
        return opts.index_path
    return join_path(home, INDEX_FILE)


def remove_if_present(path: str) -> None:
    if Path(path).exists():
        os.remove(path)


def print_build(verb: str, path: str, report: BuildReport) -> None:
    print(
        verb
        + " "
        + path
        + ": "
        + str(report.journal_lines)
        + " journal lines, "
        + str(report.written)
        + " facts written, "
        + str(report.unchanged)
        + " unchanged, "
        + str(report.ended)
        + " versions ended, "
        + str(report.bodies)
        + " read bodies ("
        + str(report.missing_bodies)
        + " missing), "
        + str(int(report.seconds * 1000))
        + " ms"
    )


def index_command(args: list[str], home: str, journal: str) -> None:
    """vinci index: rebuild, update, stats, or load a records file."""
    opts = parse_options(args)
    if len(opts.rest) == 0:
        raise user_error(
            "usage: vinci index rebuild | update | stats | embed | load <records.jsonl> [--index PATH]"
        )
    sub = opts.rest[0]
    path = index_path_of(opts, home)

    if sub == "rebuild":
        remove_if_present(path)
        store = IndexStore(path)
        report = update_from_journal(store, journal, home)
        print_build("rebuilt", path, report)
        return

    if sub == "update":
        store = IndexStore(path)
        report = update_from_journal(store, journal, home)
        print_build("updated", path, report)
        return

    if sub == "stats":
        if not Path(path).exists():
            raise user_error("no index at " + path + "; run vinci index rebuild")
        store = IndexStore(path)
        print("index " + path)
        print("  file: " + str(store.file_bytes()) + " bytes")
        print("  facts: " + str(store.fact_total()))
        print(
            "  journal: "
            + str(store.meta_int("journal_offset"))
            + " bytes read, "
            + str(store.meta_int("journal_lines"))
            + " lines"
        )
        last = store.meta_get("last_build_seconds")
        if last != "":
            print("  last build: " + str(int(float(last) * 1000)) + " ms")
        print("  " + "kind".ljust(12) + "current".ljust(9) + "total")
        total = 0
        current = 0
        for kc in store.kind_counts():
            print("  " + kc.kind.ljust(12) + str(kc.current).ljust(9) + str(kc.total))
            total += kc.total
            current += kc.current
        print("  " + "all".ljust(12) + str(current).ljust(9) + str(total))
        return

    if sub == "embed":
        if not Path(path).exists():
            raise user_error("no index at " + path + "; run vinci index rebuild")
        store = IndexStore(path)
        embed_missing(store, embed_config(opts))
        return

    if sub == "load":
        if len(opts.rest) < 2:
            raise user_error("usage: vinci index load <records.jsonl> --index PATH")
        store = IndexStore(path)
        report = load_records(store, opts.rest[1])
        print(
            "loaded "
            + path
            + ": "
            + str(report.lines)
            + " records, "
            + str(report.written)
            + " facts written, "
            + str(report.unchanged)
            + " unchanged, "
            + str(report.ended)
            + " versions ended, "
            + str(int(report.seconds * 1000))
            + " ms"
        )
        return

    raise user_error("unknown index command '" + sub + "'")


def method_of(c: Candidate) -> str:
    if c.in_lexical and c.in_vector:
        return "hybrid"
    if c.in_vector:
        return "vector"
    return "lexical"


def recall_json(query: str, r: Recall, packed: Optional[Packed]) -> str:
    """Results with content, abstain and its reason, and how the question was read. With a
    budget, the packed context, its tokens and how they were counted come after the results."""
    out = '{"query":' + json_string(query)
    out += ',"route":' + json_string(r.route)
    out += ',"complexity":' + json_string(r.complexity)
    out += ',"abstain":' + ("true" if r.abstention.abstain else "false")
    if len(r.abstention.reason) > 0:
        out += ',"reason":' + json_string(r.abstention.reason)
    if r.feed_hint > 0:
        out += ',"feedHint":' + str(r.feed_hint)
    out += ',"corrected":' + ("true" if r.corrected else "false")
    if len(r.correction_query) > 0:
        out += ',"correctionQuery":' + json_string(r.correction_query)
    out += ',"dates":['
    out += ",".join(
        '{"phrase":' + json_string(w.phrase) + ',"mode":' + json_string(w.mode)
        + ',"start":' + json_string(w.start) + ',"end":' + json_string(w.end) + "}"
        for w in r.dates
    )
    out += '],"routes":['
    out += ",".join(json_string(name) for name in r.routes)
    out += '],"results":['
    parts = []
    for res in r.results:
        item = '{"id":' + json_string(res.id)
        item += ',"type":' + json_string(res.kind)
        item += ',"kind":' + json_string(res.kind)
        item += ',"content":' + json_string(res.text)
        item += ',"at":' + json_string(res.at)
        item += ',"valid_from":' + json_string(res.valid_from)
        item += ',"score":' + format_json_double(res.fused)
        item += ',"source":' + json_string(res.source)
        item += ',"trusted":' + ("true" if res.trusted else "false")
        item += ',"retrievalMethod":' + json_string(method_of(res))
        if res.has_cosine:
            item += ',"vectorCosine":' + format_json_double(res.cosine)
        item += ',"rowid":' + str(res.rowid) + "}"
        parts.append(item)
    out += ",".join(parts)
    out += "]"
    if packed is not None:
        out += ',"budget":' + str(packed.budget)
        out += ',"packed":' + json_string(packed.text)
        out += ',"packed_tokens":' + str(packed.tokens)
        out += ',"token_method":' + json_string(packed.method)
        out += ',"packed_whole":' + str(packed.whole)
        out += ',"packed_pointers":' + str(packed.pointers)
        out += ',"packed_oversized":' + str(packed.oversized)
        out += ',"packed_omitted":' + str(packed.omitted)
    return out + "}"


def weights_from_env() -> str:
    """VINCI_RECALL_WEIGHTS as written, or empty. Eval only; see parse_weights."""
    return os.environ.get("VINCI_RECALL_WEIGHTS", "")


def recall_command(args: list[str], home: str) -> None:
    """vinci recall: the best current versions for a question, as text or JSON."""
    opts = parse_options(args)
    if len(opts.rest) == 0:
        raise user_error(
            "usage: vinci recall [--json] [--limit N] [--routes lexical]"
            " [--index PATH] [--weights lexical=W,vector=W[,k=K]] <query...>"
        )
    query = " ".join(opts.rest)
    path = index_path_of(opts, home)
    if not Path(path).exists():
        raise user_error("no index at " + path + "; run vinci index rebuild")
    store = IndexStore(path)
    now = clock_now()
    settings = Settings(opts.limit, list(opts.routes), now.utc, today_of(now), embed_config(opts))
    settings.sources = list(opts.sources)
    # Eval only: the fusion override comes from --weights, or from the environment for harnesses
    # that cannot pass a flag. Chat does not read either, so it keeps the defaults.
    if opts.weights is not None:
        settings.fusion = opts.weights
    else:
        env_weights = weights_from_env()
        if len(env_weights) > 0:
            settings.fusion = parse_weights(env_weights)
    found = recall(store, query, settings)
    packed: Optional[Packed] = None
    if opts.budget is not None:
        counter = estimate_counter()
        if opts.tokenizer_url is not None:
            counter = Counter(METHOD_SERVER, opts.tokenizer_url)
        packed = pack(found.results, opts.budget, opts.whole, counter)
    if opts.json:
        print(recall_json(query, found, packed))
        return
    if found.abstention.abstain:
        print("abstain: " + found.abstention.reason)
    if found.feed_hint > 0:
        print(feed_hint_line(found.feed_hint))
    if packed is not None:
        if len(packed.text) > 0:
            print(packed.text)
        return
    for res in found.results:
        one_line = res.text.replace("\n", " / ")
        print(res.id + "  " + res.kind + "  " + format_json_double(res.fused) + "  " + clip(one_line, 160))


def open_command(args: list[str], home: str) -> None:
    """vinci open: part of the fact or handle an id names, as text or JSON.

    The id is the first argument that is not lines= or grep=. An id can name more than one
    fact (an idea and its work lines), and each is opened in turn.
    """
    opts = parse_options(args)
    ident = ""
    req = Request()
    for a in opts.rest:
        if a.startswith("lines="):
            parse_lines_arg(a[6:], req)
        elif a.startswith("grep="):
            word = a[5:]
            if len(word) == 0:
                raise user_error("grep= needs a word, for example grep=ideas")
            req.has_grep = True
            req.word = word
        elif len(ident) == 0:
            ident = a
        else:
            raise user_error("open takes one id, got " + ident + " and " + a)
    if len(ident) == 0:
        raise user_error("usage: vinci open <id> [lines=A-B] [grep=word] [--index PATH] [--json]")
    if req.has_lines and req.has_grep:
        raise user_error("give lines= or grep=, not both")
    path = index_path_of(opts, home)
    if not Path(path).exists():
        raise user_error("no index at " + path + "; run vinci index rebuild")
    store = IndexStore(path)
    items = lookup(store, ident)
    if len(items) == 0:
        raise user_error("no fact or handle named '" + ident + "' in the index")
    views: list[str] = []
    for item in items:
        view = build_view(ident, item, req, OPEN_CAP_BYTES)
        views.append(render_json(view) if opts.json else render_text(view))
    if opts.json:
        print('{"id":' + json_string(ident) + ',"items":[' + ",".join(views) + "]}")
        return
    for i, text in enumerate(views):
        if i > 0:
            print("")
        print(text)
