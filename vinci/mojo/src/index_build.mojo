# Builds the index from the journal, and loads external records into it.
#
# Journal lines become facts by the mapping in docs/memory.md: one fact per line that
# carries text a person would recall. A read line also gets one fact per chunk of its
# body (docs/<sha>.md). One run is one transaction, and the byte offset of the last
# complete line is stored with it, so the next run starts there.

from std.pathlib import Path
from std.python import Python, PythonObject

from feed_edge import journal_prefix, journal_split
from feed_store import delete_source_rows
from index_store import Fact, IndexStore, sha256_hex
from journal import Event, parse_event
from text import ascii_tail
from timeutil import ascii_prefix, format_instant

comptime CHUNK_BYTES = 1000
comptime JOURNAL_SOURCE = "journal"


struct BuildReport(Copyable, Movable):
    """What one run did. Counts are for this run, except journal_lines and offset.
    """

    var lines: Int
    var written: Int
    var unchanged: Int
    var ended: Int
    var bodies: Int
    var missing_bodies: Int
    var journal_lines: Int
    var offset: Int
    var seconds: Float64
    var versions: Int
    var rebuilt: Bool

    def __init__(out self):
        self.lines = 0
        self.written = 0
        self.unchanged = 0
        self.ended = 0
        self.bodies = 0
        self.missing_bodies = 0
        self.journal_lines = 0
        self.offset = 0
        self.seconds = 0.0
        self.versions = 0
        self.rebuilt = False


def indexed_kind(kind: String) -> Bool:
    """Kinds whose text a person would recall. Pure state (stage, watch, spent, sync,
    used, review, tend, link, log, spec) is not indexed: it has no text to recall.
    """
    return (
        kind == "idea"
        or kind == "for"
        or kind == "goal"
        or kind == "req"
        or kind == "cut"
        or kind == "work"
        or kind == "ship"
        or kind == "retire"
        or kind == "bookmark"
        or kind == "source"
        or kind == "note"
        or kind == "home"
        or kind == "issue"
        or kind == "diagnose"
        or kind == "principle"
        or kind == "predict"
        or kind == "result"
        or kind == "commit"
        or kind == "missed"
        or kind == "kept"
        or kind == "remember"
        or kind == "memory"
        or kind == "read"
        or kind == "exchange"
    )


def versioned_kind(kind: String) -> Bool:
    """Kinds whose later line with the same id replaces the earlier one (the fold keeps
    only the last value). Their repeats are new versions. Other kinds are appended
    events, so a repeat of kind and id is a new fact with its own record key."""
    return (
        kind == "for"
        or kind == "goal"
        or kind == "cut"
        or kind == "home"
        or kind == "diagnose"
        or kind == "result"
        or kind == "remember"
        or kind == "memory"
    )


def memory_kind(kind: String) -> Bool:
    return kind == "remember" or kind == "memory"


def idea_kind(kind: String) -> Bool:
    """Kinds whose id is an idea id."""
    return (
        kind == "idea"
        or kind == "for"
        or kind == "goal"
        or kind == "home"
        or kind == "work"
        or kind == "ship"
        or kind == "retire"
    )


def content_of(e: Event) raises -> String:
    """The text a recall returns: the line's text, with the part a person searches
    for: a bookmark's why and url, a source's or read's url, a work line's commit.
    """
    var kind = e.kind
    var text = e.text.copy().or_else("")
    if kind == "bookmark":
        var why = e.why.copy().or_else("")
        if why.byte_length() > 0:
            text += "\n" + why
    if kind == "work":
        # A work line's where is the commit it landed in. People search for commit ids.
        var commit = e.where_.copy().or_else("")
        if commit.byte_length() > 0:
            text += "\ncommit " + commit
    if kind == "bookmark" or kind == "source" or kind == "read":
        var url = e.url.copy().or_else("")
        if url.byte_length() > 0:
            text += "\n" + url
    return text


def trusted_of(e: Event) raises -> Bool:
    """The user's own lines are trusted. An assistant's exchange is not the user's
    writing, and read bodies are chunked separately as untrusted."""
    if e.kind == "exchange":
        return e.who.copy().or_else("") == "user"
    return True


def status_of(e: Event) raises -> String:
    """A memory line's status tag (archived, retracted, superseded), else active.
    """
    if not memory_kind(e.kind):
        return "active"
    for tag in e.tags.copy().or_else(List[String]()):
        var t = String(tag)
        if t == "retracted" or t == "superseded" or t == "archived":
            return t
    return "active"


def first_idea_ref(refs: List[String]) -> Optional[String]:
    """The first ref that is an idea id (ideas are i-prefixed)."""
    for r in refs:
        var target = String(r)
        if target.startswith("i"):
            return Optional[String](target)
    return None


def idea_of(e: Event) raises -> Optional[String]:
    """The idea a line belongs to: its own id for idea kinds, else the first
    idea ref, else a from:<idea id> tag."""
    var id = e.id.copy().or_else("")
    if idea_kind(e.kind) and id.startswith("i"):
        return Optional[String](id)
    var refs = e.refs.copy().or_else(List[String]())
    var from_refs = first_idea_ref(refs)
    if from_refs is not None:
        return from_refs
    for tag in e.tags.copy().or_else(List[String]()):
        var t = String(tag)
        if t.startswith("from:") and t.byte_length() > 5:
            var rest = String(ascii_tail(t, 5))
            if rest.startswith("i"):
                return Optional[String](rest)
    return None


def chunk_body(body: String) raises -> List[String]:
    """A read body in chunks of about CHUNK_BYTES bytes, cut at line boundaries.

    A markdown heading always starts a new chunk, so headings act as sections. A
    line longer than CHUNK_BYTES is kept whole. Blank-only chunks are dropped.
    """
    var chunks = List[String]()
    var cur = String()
    for raw in body.split("\n"):
        var line = String(raw)
        var heading = line.startswith("#")
        if cur.byte_length() > 0 and (
            heading or cur.byte_length() + line.byte_length() + 1 > CHUNK_BYTES
        ):
            if String(cur.strip()).byte_length() > 0:
                chunks.append(cur.copy())
            cur = String()
        if cur.byte_length() > 0:
            cur += "\n"
        cur += line
    if String(cur.strip()).byte_length() > 0:
        chunks.append(cur.copy())
    return chunks^


def join_path(dir: String, name: String) -> String:
    if dir.endswith("/"):
        return dir + name
    return dir + "/" + name


def index_event(
    mut store: IndexStore,
    e: Event,
    line: Int,
    line_hash: String,
    home: String,
    mut report: BuildReport,
) raises:
    """Store one journal line as facts, applying its corrections first."""
    var kind = e.kind
    if not indexed_kind(kind):
        return
    var text = content_of(e)
    if text.byte_length() == 0 and kind != "read":
        return
    var at = format_instant(e.at)
    var id = e.id.copy().or_else("")
    var refs = e.refs.copy().or_else(List[String]())

    # A memory line's refs name the memories it supersedes: k ids are remember
    # records, m ids are memory records. The superseded version ends at this line.
    if memory_kind(kind):
        for r in refs:
            var target = String(r)
            if target.startswith("k"):
                report.ended += store.end_version(
                    JOURNAL_SOURCE, "remember:" + target, at
                )
            elif target.startswith("m"):
                report.ended += store.end_version(
                    JOURNAL_SOURCE, "memory:" + target, at
                )

    # Record key: kind and id, so an idea and a work line with the same id stay
    # apart. A repeat of an appended kind gets its line number in its key.
    var record_id: String
    var cite: String
    if id.byte_length() == 0:
        record_id = kind + ":L" + String(line)
        cite = "L" + String(line)
    else:
        cite = id
        var bare = kind + ":" + id
        if versioned_kind(kind) or not store.has_key(JOURNAL_SOURCE, bare):
            record_id = bare
        else:
            record_id = bare + "#L" + String(line)

    var idea = idea_of(e)
    var trusted = trusted_of(e)
    var status = status_of(e)
    var tags = e.tags.copy().or_else(List[String]())
    var fact = Fact(
        JOURNAL_SOURCE,
        record_id,
        line_hash,
        kind,
        text,
        cite,
        at,
        idea^,
        tags^,
        refs.copy(),
        trusted,
        status,
        line,
    )
    var code = store.put(fact)
    if code == 0:
        report.unchanged += 1
    else:
        report.written += 1
        if code == 2:
            report.ended += 1
            report.versions += 1

    if kind == "read":
        index_read_body(store, e, record_id, cite, at, line, home, report)


def index_read_body(
    mut store: IndexStore,
    e: Event,
    title_record: String,
    title_cite: String,
    at: String,
    line: Int,
    home: String,
    mut report: BuildReport,
) raises:
    """One untrusted fact per chunk of the read line's body."""
    var where_ = e.where_.copy().or_else("")
    if where_.byte_length() == 0:
        report.missing_bodies += 1
        return
    var path = join_path(home, where_)
    if not Path(path).exists():
        report.missing_bodies += 1
        return
    report.bodies += 1
    var chunks = chunk_body(Path(path).read_text())
    for n in range(len(chunks)):
        var chunk = chunks[n].copy()
        var number = String(n + 1)
        var refs = List[String]()
        refs.append(title_cite)
        var fact = Fact(
            JOURNAL_SOURCE,
            title_record + "/" + number,
            sha256_hex(chunk),
            "read",
            chunk,
            title_cite + "/" + number,
            at,
            None,
            List[String](),
            refs^,
            False,
            "active",
            line,
        )
        var code = store.put(fact)
        if code == 0:
            report.unchanged += 1
        else:
            report.written += 1
            if code == 2:
                report.ended += 1
                report.versions += 1


def strip_cr(line: String) -> String:
    """A line without its trailing carriage return, if it has one."""
    var n = line.byte_length()
    if n > 0 and line.as_bytes()[n - 1] == 13:
        return ascii_prefix(line, n - 1)
    return line.copy()


def read_bytes_from(path: String, offset: Int) raises -> String:
    """The bytes of a file from offset to its end, decoded as UTF-8. Empty if missing.
    """
    if not Path(path).exists():
        return String()
    var builtins = Python.import_module("builtins")
    var fh = builtins.open(PythonObject(path), PythonObject("rb"))
    _ = fh.seek(PythonObject(offset))
    var data = fh.read()
    fh.close()
    return String(data.decode(PythonObject("utf-8")))


def file_bytes(path: String) raises -> Int:
    if not Path(path).exists():
        return 0
    var os = Python.import_module("os")
    return Int(String(os.path.getsize(PythonObject(path))))


def update_from_journal(
    mut store: IndexStore, journal: String, home: String
) raises -> BuildReport:
    """Index the journal lines after the stored offset. A full rebuild is an empty store.

    The journal's cursor is its byte offset and the sha-256 of the bytes before it. When
    those bytes changed (the journal was rewritten), the journal's facts are dropped and it
    is read again from the start, and the report says so. A journal shorter than the offset
    is refused: it was truncated, which is for the person to decide (vinci feed rebuild).

    Only complete lines are read: a last line without its newline is left for the next run,
    in case the C# binary is still writing it.
    """
    var report = BuildReport()
    var time = Python.import_module("time")
    var started = Float64(String(time.perf_counter()))
    var offset = store.meta_int("journal_offset")
    var lines_before = store.meta_int("journal_lines")
    if file_bytes(journal) < offset:
        raise Error(
            "index: the journal is shorter than the index's offset; run"
            " vinci feed rebuild journal"
        )
    var split = journal_split(journal, offset)
    if split.status == "error":
        raise Error("index: cannot read the journal: " + split.prefix)
    var stored = store.meta_get("journal_prefix")

    store.begin()
    try:
        if offset > 0 and stored == "":
            # An index made before the prefix was kept: this journal's prefix is the reference.
            store.meta_set("journal_prefix", split.prefix)
            stored = split.prefix
        if offset > 0 and stored != split.prefix:
            delete_source_rows(store, JOURNAL_SOURCE)
            report.rebuilt = True
            offset = 0
            lines_before = 0
            store.meta_set("journal_offset", "0")
            store.meta_set("journal_lines", "0")
            store.meta_set("journal_prefix", "")
            split = journal_split(journal, 0)
        var text = String()
        if split.status == "ok":
            text = split.text.copy()
        var parts = List[String]()
        for raw in text.split("\n"):
            parts.append(String(raw))
        var consumed = 0
        var lineno = lines_before
        for i in range(len(parts)):
            if i == len(parts) - 1:
                break  # the tail after the last newline: incomplete or empty
            var piece = parts[i].copy()
            consumed += piece.byte_length() + 1
            lineno += 1
            report.lines += 1
            var line = strip_cr(piece)
            if line.byte_length() == 0:
                continue
            try:
                var e = parse_event(line)
                index_event(store, e, lineno, sha256_hex(line), home, report)
            except err:
                raise Error(
                    "index: journal line " + String(lineno) + ": " + String(err)
                )
        report.offset = offset + consumed
        report.journal_lines = lineno
        var seconds = Float64(String(time.perf_counter())) - started
        report.seconds = seconds
        # Nothing new means nothing to write: an update with no new lines costs no fsync.
        if consumed > 0:
            store.meta_set("journal_offset", String(report.offset))
            store.meta_set("journal_lines", String(lineno))
            store.meta_set("last_build_seconds", String(seconds))
            store.meta_set(
                "journal_prefix", journal_prefix(journal, report.offset)
            )
    except err:
        store.rollback()
        raise Error(String(err))
    store.commit()
    return report^


def load_records(mut store: IndexStore, path: String) raises -> BuildReport:
    """Load external records, one JSON object per line.

    Each object has source, id, at, kind, text, hash, trusted and links (a list of
    ids it refers to). The hash is the record's version: a missing one is the
    SHA-256 of the text. Same versioning rules as the journal, keyed by (source, id).
    """
    var report = BuildReport()
    var time = Python.import_module("time")
    var started = Float64(String(time.perf_counter()))
    var json = Python.import_module("json")
    var text = read_bytes_from(path, 0)
    var parts = List[String]()
    for raw in text.split("\n"):
        parts.append(String(raw))
    store.begin()
    try:
        for i in range(len(parts)):
            var line = String(parts[i].strip())
            if line.byte_length() == 0:
                continue
            report.lines += 1
            var obj = json.loads(PythonObject(line))
            var source = String(obj["source"])
            var id = String(obj["id"])
            var kind = String(obj["kind"])
            var body = String(obj["text"])
            var hash_value = obj.get(PythonObject("hash"), Python.none())
            var version_hash = sha256_hex(body)
            if Bool(hash_value != Python.none()):
                version_hash = String(hash_value)
            # A missing trusted field is untrusted: only the user's own writing is trusted.
            var trusted = (
                String(obj.get(PythonObject("trusted"), Python.none()))
                == "True"
            )
            var links = obj.get(PythonObject("links"), Python.none())
            var refs = List[String]()
            if Bool(links != Python.none()):
                for link in links:
                    refs.append(String(link))
            var idea = first_idea_ref(refs)
            var fact = Fact(
                source,
                id,
                version_hash,
                kind,
                body,
                id,
                String(obj["at"]),
                idea^,
                List[String](),
                refs^,
                trusted,
                "active",
                i + 1,
            )
            var code = store.put(fact)
            if code == 0:
                report.unchanged += 1
            else:
                report.written += 1
                if code == 2:
                    report.ended += 1
    except err:
        store.rollback()
        raise Error("index load: " + String(err))
    report.seconds = Float64(String(time.perf_counter())) - started
    store.commit()
    return report^
