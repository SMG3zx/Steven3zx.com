# The index and recall commands: `vinci index rebuild|update|stats|load` and
# `vinci recall [--json] [--limit N] [--routes lexical] [--index PATH] <query...>`.
# Neither changes the journal. Index files live beside it, or at --index PATH.

from std.pathlib import Path
from std.python import Python, PythonObject

from index_build import BuildReport, load_records, update_from_journal
from index_store import IndexStore
from jsonline import format_json_double, json_string
from recall import DEFAULT_ROUTES, Recall, Result, recall
from session import user_error
from text import clip, pad_right

comptime INDEX_FILE = "index.sqlite"
comptime DEFAULT_LIMIT = 10


@fieldwise_init
struct Options(Movable):
    var index_path: Optional[String]
    var limit: Int
    var routes: List[String]
    var json: Bool
    var rest: List[String]


def join_path(dir: String, name: String) -> String:
    if dir.endswith("/"):
        return dir + name
    return dir + "/" + name


def parse_options(args: List[String]) raises -> Options:
    """Flags may come anywhere. Everything else is a positional argument."""
    var opts = Options(
        None, DEFAULT_LIMIT, List[String](), False, List[String]()
    )
    var i = 0
    while i < len(args):
        var a = args[i]
        if a == "--json":
            opts.json = True
        elif a == "--index" or a == "--limit" or a == "--routes":
            if i + 1 >= len(args):
                raise user_error(a + " needs a value")
            var value = args[i + 1]
            i += 1
            if a == "--index":
                opts.index_path = Optional[String](value)
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
        opts.routes.append(DEFAULT_ROUTES)
    return opts^


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
            "usage: vinci index rebuild | update | stats | load <records.jsonl>"
            " [--index PATH]"
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


def recall_json(query: String, r: Recall) raises -> String:
    """The shape personal_baseline reads: results with content, and abstain."""
    var out = String('{"query":') + json_string(query)
    out += ',"abstain":' + ("true" if r.abstain else "false")
    out += ',"routes":['
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
        out += ',"score":' + format_json_double(res.score)
        out += ',"source":' + json_string(res.source)
        out += ',"trusted":' + ("true" if res.trusted else "false")
        out += ',"retrievalMethod":' + json_string(res.route)
        out += ',"rowid":' + String(res.rowid) + "}"
    return out + "]}"


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
    var found = recall(store, query, opts.limit, opts.routes)
    if opts.json:
        print(recall_json(query, found))
        return
    for res in found.results:
        var one_line = String(res.text.replace("\n", " / "))
        print(
            res.id
            + "  "
            + res.kind
            + "  "
            + format_json_double(res.score)
            + "  "
            + clip(one_line, 160)
        )
