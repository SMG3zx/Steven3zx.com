# The index and recall commands: `vinci index rebuild|update|stats|load|embed` and
# `vinci recall [--json] [--limit N] [--routes lexical,vector] [--index PATH]
#  [--label L] [--base-url U] [--cache PATH] [--budget N [--whole N] [--tokenizer-url U]]
#  <query...>`, and `vinci open <id> [lines=A-B] [grep=word] [--index PATH] [--json]`.
# None of them changes the journal. Index files live beside it, or at --index PATH.

from std.pathlib import Path
from std.python import Python, PythonObject

from dates import clock_now, today_of
from embed_edge import EmbedConfig, default_config
from fusion import Candidate
from handles import lookup
from index_build import BuildReport, load_records, update_from_journal
from index_store import IndexStore
from jsonline import format_json_double, json_string
from open_view import (
    build_view,
    no_request,
    parse_lines_arg,
    render_json,
    render_text,
)
from pack import (
    DEFAULT_WHOLE,
    METHOD_SERVER,
    Counter,
    Packed,
    estimate_counter,
    pack,
)
from recall import DEFAULT_ROUTES, Recall, Settings, parse_sources, recall
from session import user_error
from text import ascii_tail, clip, pad_right
from vectors import embed_missing

comptime INDEX_FILE = "index.sqlite"
comptime DEFAULT_LIMIT = 10
comptime OPEN_CAP_BYTES = 4000


@fieldwise_init
struct Options(Movable):
    var index_path: Optional[String]
    var limit: Int
    var routes: List[String]
    var json: Bool
    var rest: List[String]
    var label: Optional[String]
    var base_url: Optional[String]
    var cache: Optional[String]
    var model: Optional[String]
    var fallback_label: Optional[String]
    var fallback_url: Optional[String]
    var budget: Optional[Int]
    var whole: Int
    var tokenizer_url: Optional[String]
    var sources: List[String]


def join_path(dir: String, name: String) -> String:
    if dir.endswith("/"):
        return dir + name
    return dir + "/" + name


def parse_options(args: List[String]) raises -> Options:
    """Flags may come anywhere. Everything else is a positional argument."""
    var opts = Options(
        None,
        DEFAULT_LIMIT,
        List[String](),
        False,
        List[String](),
        None,
        None,
        None,
        None,
        None,
        None,
        None,
        DEFAULT_WHOLE,
        None,
        List[String](),
    )
    var i = 0
    while i < len(args):
        var a = args[i]
        if a == "--json":
            opts.json = True
        elif (
            a == "--index"
            or a == "--limit"
            or a == "--routes"
            or a == "--label"
            or a == "--base-url"
            or a == "--cache"
            or a == "--model"
            or a == "--query-fallback-label"
            or a == "--query-fallback-url"
            or a == "--budget"
            or a == "--whole"
            or a == "--tokenizer-url"
            or a == "--sources"
        ):
            if i + 1 >= len(args):
                raise user_error(a + " needs a value")
            var value = args[i + 1]
            i += 1
            if a == "--index":
                opts.index_path = Optional[String](value)
            elif a == "--label":
                opts.label = Optional[String](value)
            elif a == "--base-url":
                opts.base_url = Optional[String](value)
            elif a == "--cache":
                opts.cache = Optional[String](value)
            elif a == "--model":
                opts.model = Optional[String](value)
            elif a == "--query-fallback-label":
                opts.fallback_label = Optional[String](value)
            elif a == "--query-fallback-url":
                opts.fallback_url = Optional[String](value)
            elif a == "--tokenizer-url":
                opts.tokenizer_url = Optional[String](value)
            elif a == "--sources":
                opts.sources = parse_sources(value)
            elif a == "--budget":
                try:
                    opts.budget = Optional[Int](Int(value))
                except:
                    raise user_error("--budget must be a whole number")
                if opts.budget.value() < 1:
                    raise user_error("--budget must be at least 1")
            elif a == "--whole":
                try:
                    opts.whole = Int(value)
                except:
                    raise user_error("--whole must be a whole number")
                if opts.whole < 0:
                    raise user_error("--whole must be 0 or more")
            elif a == "--limit":
                try:
                    opts.limit = Int(value)
                except:
                    raise user_error("--limit must be a whole number")
                if opts.limit <= 0:
                    raise user_error("--limit must be at least 1")
            else:
                opts.routes = List[String]()
                for name in value.split(","):
                    opts.routes.append(String(name))
        else:
            opts.rest.append(a)
        i += 1
    if len(opts.routes) == 0:
        for name in DEFAULT_ROUTES.split(","):
            opts.routes.append(String(name))
    if len(opts.sources) == 0:
        opts.sources.append("journal")
    return opts^


def embed_config(opts: Options) raises -> EmbedConfig:
    """The embedding settings: the environment's, with any flag replacing one.
    """
    var cfg = default_config()
    if opts.model is not None:
        cfg.model = opts.model.value()
        cfg.label = opts.model.value()
    if opts.label is not None:
        cfg.label = opts.label.value()
    if opts.base_url is not None:
        cfg.base_url = opts.base_url.value()
    if opts.cache is not None:
        cfg.cache = opts.cache.value()
    if opts.fallback_url is not None:
        var label = String()
        if opts.fallback_label is not None:
            label = opts.fallback_label.value()
        cfg.set_fallback(label^, opts.fallback_url.value())
    return cfg^


def index_path_of(opts: Options, home: String) raises -> String:
    if opts.index_path is not None:
        return opts.index_path.value()
    return join_path(home, INDEX_FILE)


def remove_if_present(path: String) raises:
    if Path(path).exists():
        var os = Python.import_module("os")
        _ = os.remove(PythonObject(path))


def print_build(verb: String, path: String, report: BuildReport):
    print(
        verb
        + " "
        + path
        + ": "
        + String(report.journal_lines)
        + " journal lines, "
        + String(report.written)
        + " facts written, "
        + String(report.unchanged)
        + " unchanged, "
        + String(report.ended)
        + " versions ended, "
        + String(report.bodies)
        + " read bodies ("
        + String(report.missing_bodies)
        + " missing), "
        + String(Int(report.seconds * 1000))
        + " ms"
    )


def index_command(args: List[String], home: String, journal: String) raises:
    """vinci index: rebuild, update, stats, or load a records file."""
    var opts = parse_options(args)
    if len(opts.rest) == 0:
        raise user_error(
            "usage: vinci index rebuild | update | stats | embed | load"
            " <records.jsonl> [--index PATH]"
        )
    var sub = opts.rest[0]
    var path = index_path_of(opts, home)

    if sub == "rebuild":
        remove_if_present(path)
        var store = IndexStore(path)
        var report = update_from_journal(store, journal, home)
        print_build("rebuilt", path, report)
        return

    if sub == "update":
        var store = IndexStore(path)
        var report = update_from_journal(store, journal, home)
        print_build("updated", path, report)
        return

    if sub == "stats":
        if not Path(path).exists():
            raise user_error(
                "no index at " + path + "; run vinci index rebuild"
            )
        var store = IndexStore(path)
        print("index " + path)
        print("  file: " + String(store.file_bytes()) + " bytes")
        print("  facts: " + String(store.fact_total()))
        print(
            "  journal: "
            + String(store.meta_int("journal_offset"))
            + " bytes read, "
            + String(store.meta_int("journal_lines"))
            + " lines"
        )
        var last = store.meta_get("last_build_seconds")
        if last != "":
            print("  last build: " + String(Int(Float64(last) * 1000)) + " ms")
        print("  " + pad_right("kind", 12) + pad_right("current", 9) + "total")
        var total = 0
        var current = 0
        for kc in store.kind_counts():
            print(
                "  "
                + pad_right(kc.kind, 12)
                + pad_right(String(kc.current), 9)
                + String(kc.total)
            )
            total += kc.total
            current += kc.current
        print(
            "  "
            + pad_right("all", 12)
            + pad_right(String(current), 9)
            + String(total)
        )
        return

    if sub == "embed":
        if not Path(path).exists():
            raise user_error(
                "no index at " + path + "; run vinci index rebuild"
            )
        var store = IndexStore(path)
        _ = embed_missing(store, embed_config(opts))
        return

    if sub == "load":
        if len(opts.rest) < 2:
            raise user_error(
                "usage: vinci index load <records.jsonl> --index PATH"
            )
        var store = IndexStore(path)
        var report = load_records(store, opts.rest[1])
        print(
            "loaded "
            + path
            + ": "
            + String(report.lines)
            + " records, "
            + String(report.written)
            + " facts written, "
            + String(report.unchanged)
            + " unchanged, "
            + String(report.ended)
            + " versions ended, "
            + String(Int(report.seconds * 1000))
            + " ms"
        )
        return

    raise user_error("unknown index command '" + sub + "'")


def recall_json(
    query: String, r: Recall, packed: Optional[Packed]
) raises -> String:
    """Results with content, abstain and its reason, and how the question was read.
    personal_baseline and index_eval read results (content) and abstain. With a budget,
    the packed context, its tokens and how they were counted come after the results.
    """
    var out = String('{"query":') + json_string(query)
    out += ',"route":' + json_string(r.route)
    out += ',"complexity":' + json_string(r.complexity)
    out += ',"abstain":' + ("true" if r.abstention.abstain else "false")
    if r.abstention.reason.byte_length() > 0:
        out += ',"reason":' + json_string(r.abstention.reason)
    out += ',"corrected":' + ("true" if r.corrected else "false")
    if r.correction_query.byte_length() > 0:
        out += ',"correctionQuery":' + json_string(r.correction_query)
    out += ',"dates":['
    for i in range(len(r.dates)):
        if i > 0:
            out += ","
        var w = r.dates[i].copy()
        out += (
            '{"phrase":'
            + json_string(w.phrase)
            + ',"mode":'
            + json_string(w.mode)
            + ',"start":'
            + json_string(w.start)
            + ',"end":'
            + json_string(w.end)
            + "}"
        )
    out += '],"routes":['
    for i in range(len(r.routes)):
        if i > 0:
            out += ","
        out += json_string(r.routes[i])
    out += '],"results":['
    for i in range(len(r.results)):
        var res = r.results[i].copy()
        if i > 0:
            out += ","
        out += '{"id":' + json_string(res.id)
        out += ',"type":' + json_string(res.kind)
        out += ',"kind":' + json_string(res.kind)
        out += ',"content":' + json_string(res.text)
        out += ',"at":' + json_string(res.at)
        out += ',"valid_from":' + json_string(res.valid_from)
        out += ',"score":' + format_json_double(res.fused)
        out += ',"source":' + json_string(res.source)
        out += ',"trusted":' + ("true" if res.trusted else "false")
        out += ',"retrievalMethod":' + json_string(method_of(res))
        if res.has_cosine:
            out += ',"vectorCosine":' + format_json_double(res.cosine)
        out += ',"rowid":' + String(res.rowid) + "}"
    out += "]"
    if packed is not None:
        var p = packed.value().copy()
        out += ',"budget":' + String(p.budget)
        out += ',"packed":' + json_string(p.text)
        out += ',"packed_tokens":' + String(p.tokens)
        out += ',"token_method":' + json_string(p.method)
        out += ',"packed_whole":' + String(p.whole)
        out += ',"packed_pointers":' + String(p.pointers)
        out += ',"packed_oversized":' + String(p.oversized)
        out += ',"packed_omitted":' + String(p.omitted)
    return out + "}"


def method_of(c: Candidate) -> String:
    if c.in_lexical and c.in_vector:
        return "hybrid"
    if c.in_vector:
        return "vector"
    return "lexical"


def recall_command(args: List[String], home: String) raises:
    """vinci recall: the best current versions for a question, as text or JSON.
    """
    var opts = parse_options(args)
    if len(opts.rest) == 0:
        raise user_error(
            "usage: vinci recall [--json] [--limit N] [--routes lexical]"
            " [--index PATH] <query...>"
        )
    var query = String()
    for i in range(len(opts.rest)):
        if i > 0:
            query += " "
        query += opts.rest[i]
    var path = index_path_of(opts, home)
    if not Path(path).exists():
        raise user_error("no index at " + path + "; run vinci index rebuild")
    var store = IndexStore(path)
    var now = clock_now()
    var settings = Settings(
        opts.limit,
        opts.routes.copy(),
        now.utc,
        today_of(now),
        embed_config(opts),
    )
    settings.sources = opts.sources.copy()
    var found = recall(store, query, settings)
    var packed = Optional[Packed](None)
    if opts.budget is not None:
        var counter = estimate_counter()
        if opts.tokenizer_url is not None:
            counter = Counter(String(METHOD_SERVER), opts.tokenizer_url.value())
        packed = Optional[Packed](
            pack(found.results, opts.budget.value(), opts.whole, counter)
        )
    if opts.json:
        print(recall_json(query, found, packed))
        return
    if found.abstention.abstain:
        print("abstain: " + found.abstention.reason)
    if packed is not None:
        var p = packed.value().copy()
        if p.text.byte_length() > 0:
            print(p.text)
        return
    for res in found.results:
        var one_line = String(res.text.replace("\n", " / "))
        print(
            res.id
            + "  "
            + res.kind
            + "  "
            + format_json_double(res.fused)
            + "  "
            + clip(one_line, 160)
        )


def open_command(args: List[String], home: String) raises:
    """vinci open: part of the fact or handle an id names, as text or JSON.

    The id is the first argument that is not lines= or grep=. An id can name more than
    one fact (an idea and its work lines), and each is opened in turn.
    """
    var opts = parse_options(args)
    var id = String()
    var req = no_request()
    for a in opts.rest:
        if a.startswith("lines="):
            parse_lines_arg(String(ascii_tail(a, 6)), req)
        elif a.startswith("grep="):
            var word = String(ascii_tail(a, 5))
            if word.byte_length() == 0:
                raise user_error("grep= needs a word, for example grep=ideas")
            req.has_grep = True
            req.word = word
        elif id.byte_length() == 0:
            id = a
        else:
            raise user_error("open takes one id, got " + id + " and " + a)
    if id.byte_length() == 0:
        raise user_error(
            "usage: vinci open <id> [lines=A-B] [grep=word] [--index PATH]"
            " [--json]"
        )
    if req.has_lines and req.has_grep:
        raise user_error("give lines= or grep=, not both")
    var path = index_path_of(opts, home)
    if not Path(path).exists():
        raise user_error("no index at " + path + "; run vinci index rebuild")
    var store = IndexStore(path)
    var items = lookup(store, id)
    if len(items) == 0:
        raise user_error("no fact or handle named '" + id + "' in the index")
    var views = List[String]()
    for item in items:
        var view = build_view(id, item, req, OPEN_CAP_BYTES)
        if opts.json:
            views.append(render_json(view))
        else:
            views.append(render_text(view))
    if opts.json:
        var body = String()
        for i in range(len(views)):
            if i > 0:
                body += ","
            body += views[i]
        print('{"id":' + json_string(id) + ',"items":[' + body + "]}")
        return
    for i in range(len(views)):
        if i > 0:
            print("")
        print(views[i])
