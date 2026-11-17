# `vinci memory import <snapshot.json>`: the one-time move of the memory service's
# records into the journal. The mapping is in docs/memory.md, section "Journal
# kinds for memory (mark 2)". The snapshot comes from `vinci memory backup`,
# which only reads Neo4j. Lines are written through Session.record, so the
# journal serializer is the only writer. Bodies of read documents go to
# $VINCI_HOME/docs/<sha256>.md.
#
# Idempotent: a record whose source id is already in a `where` field of a
# journal line is not written again, so a second run appends nothing.
# Journal copies of facts (source "vinci-journal") are never imported: the
# journal already holds them.

from std.collections import Dict
from std.os import makedirs
from std.pathlib import Path
from std.python import Python, PythonObject

from journal import Event, directory_of
from session import Session, user_error
from text import ascii_tail
from timeutil import Instant, ascii_prefix, parse_instant

comptime SNAPSHOT_FORMAT = "personal-memory-snapshot"
comptime NEO_PREFIX = "neo4j:"
comptime READ_MARK = " (read "


@fieldwise_init
struct Pending(Copyable, Movable):
    """One record to write, before its id and its refs are known."""

    var kind: String
    var source_id: String
    var at: Instant
    var text: String
    var value: Optional[Float64]
    var who: Optional[String]
    var why: Optional[String]
    var url: Optional[String]
    var where_: String
    var tags: List[String]
    var journal_refs: List[String]
    var source_refs: List[String]
    var body: String


def is_test_project(project: String) -> Bool:
    """Projects the evaluation seeds write into (src/Memory/Evaluation)."""
    return (
        project == "benchmark-suite"
        or project == "paraphrase-suite"
        or project.startswith("benchmark-isolation")
    )


def is_test_source(source: String) -> Bool:
    """Sources the evaluation and its fixtures write under."""
    return (
        source == "test"
        or source == "benchmark"
        or source == "benchmark-fixture"
    )


def is_imported_kind(kind: String) -> Bool:
    return (
        kind == "remember"
        or kind == "memory"
        or kind == "read"
        or kind == "exchange"
    )


def kind_order() -> List[String]:
    """The order the summary lists kinds in."""
    var out = List[String]()
    out.append("remember")
    out.append("memory")
    out.append("read")
    out.append("exchange")
    return out^


def text_of(obj: PythonObject) raises -> String:
    """A Python value as text. None is the empty string."""
    if Bool(obj == Python.none()):
        return String()
    return String(obj)


def prop(node: PythonObject, name: String) raises -> String:
    """A property of a snapshot node, as text."""
    return text_of(node["properties"].get(PythonObject(name), Python.none()))


def metadata_field(node: PythonObject, name: String) raises -> String:
    """A field of a node's metadata. The snapshot stores metadata as a JSON string.
    """
    var raw = node["properties"].get(PythonObject("metadata"), Python.none())
    if Bool(raw == Python.none()):
        return String()
    var json = Python.import_module("json")
    var meta = raw
    try:
        meta = json.loads(raw)
    except:
        pass
    return text_of(meta.get(PythonObject(name), Python.none()))


def list_of(d: Dict[String, List[String]], key: String) raises -> List[String]:
    """The list stored under key, or an empty list."""
    var out = List[String]()
    if key in d:
        for item in d[key]:
            out.append(item)
    return out^


def append_to(
    mut d: Dict[String, List[String]], key: String, value: String
) raises:
    """Add value to the list under key, creating it when needed."""
    if key in d:
        d[key].append(value)
    else:
        var fresh = List[String]()
        fresh.append(value)
        d[key] = fresh^


def prefix_for(kind: String) raises -> String:
    """The id letter for each kind. state.mojo's next_id covers i r n s x p h b, so these do not clash.
    """
    if kind == "remember":
        return "k"
    if kind == "memory":
        return "m"
    if kind == "read":
        return "d"
    if kind == "exchange":
        return "e"
    raise Error("memory import: no id prefix for " + kind)


def sort_order(values: List[Instant]) -> List[Int]:
    """Indices of values, earliest first. A stable insertion sort."""
    var order = List[Int]()
    for i in range(len(values)):
        var at = len(order)
        var utc = values[i].utc
        while at > 0 and values[order[at - 1]].utc > utc:
            at -= 1
        order.insert(at, i)
    return order^


def counts_json(
    kinds: List[String], counts: Dict[String, Int]
) raises -> String:
    """The counts as JSON members, one per kind that has any."""
    var out = String()
    var first = True
    for kind in kinds:
        if kind in counts:
            if not first:
                out += ","
            out += '"' + kind + '":' + String(counts[kind])
            first = False
    return out


def memory_import(mut s: Session, args: List[String]) raises:
    """Write the snapshot's remember, memory, read and exchange records to the journal.
    """
    if len(args) == 0:
        raise user_error("missing <snapshot.json>")
    var path = args[0]
    if not Path(path).exists():
        raise user_error("no snapshot at " + path)

    var json = Python.import_module("json")
    var snap = json.loads(PythonObject(Path(path).read_text()))
    if String(snap["format"]) != SNAPSHOT_FORMAT:
        raise user_error(path + " is not a memory snapshot")
    var nodes = snap["nodes"]
    var rels = snap["relationships"]
    var node_count = Int(String(len(nodes)))
    var rel_count = Int(String(len(rels)))

    # Index the snapshot: node position by key, and the relationships the mapping reads.
    var node_at = Dict[String, Int]()
    for i in range(node_count):
        node_at[String(nodes[i]["key"])] = i
    var supports = Dict[
        String, List[String]
    ]()  # Memory -> the Events it came from
    var supersedes = Dict[
        String, List[String]
    ]()  # newer Memory -> the older ones it replaces
    var message_of_event = Dict[
        String, String
    ]()  # Event -> the Message it represents
    var event_of_message = Dict[
        String, String
    ]()  # Message -> the Event that carries it
    var chunks_of = Dict[String, List[String]]()  # Document -> its Chunks
    for j in range(rel_count):
        var rel = rels[j]
        var rtype = String(rel["type"])
        var src = String(rel["sourceKey"])
        var dst = String(rel["targetKey"])
        if rtype == "SUPPORTED_BY":
            append_to(supports, src, dst)
        elif rtype == "SUPERSEDES":
            append_to(supersedes, src, dst)
        elif rtype == "REPRESENTS":
            message_of_event[dst] = src
            event_of_message[src] = dst
        elif rtype == "HAS_CHUNK":
            append_to(chunks_of, src, dst)

    # Ids the journal holds now. A copy can name an id from an older journal that is no longer here.
    var journal_now = s.journal.read()
    var journal_ids = Dict[String, Bool]()
    for e in journal_now:
        if e.id is not None:
            journal_ids[e.id.value()] = True

    # What is left out, so the summary can say so.
    var skipped_test = 0
    var journal_copies = 0
    var events_unmapped = 0
    var refs_dropped = 0
    var labels_not_mapped = Dict[String, Int]()

    var pending = List[Pending]()
    for i in range(node_count):
        var node = nodes[i]
        var key = String(node["key"])
        var label = String(node["labels"][0])

        if label == "Memory":
            var sup = list_of(supports, key)
            var all_test = len(sup) > 0
            for ek in sup:
                if not is_test_source(prop(nodes[node_at[ek]], "source")):
                    all_test = False
            if is_test_project(prop(node, "projectId")) or all_test:
                skipped_test += 1
                continue
            var kind = String("memory") if len(sup) > 0 else String("remember")
            var journal_refs = List[String]()
            var source_refs = List[String]()
            var tags = List[String]()
            tags.append(prop(node, "category"))
            var status = prop(node, "status")
            if status != "active":
                tags.append(status)
            # The date of a memory is the earliest date of the events it came from. validFrom
            # is when consolidation ran, which is not when the fact happened.
            var when = parse_instant(prop(node, "validFrom"))
            for ek in sup:
                var ev = nodes[node_at[ek]]
                var occurred = prop(ev, "occurredAt")
                if occurred.byte_length() > 0:
                    var earlier = parse_instant(occurred)
                    if earlier.utc < when.utc:
                        when = earlier.copy()
                if prop(ev, "source") == "vinci-journal":
                    var jid = metadata_field(ev, "journalId")
                    if jid in journal_ids:
                        # An idea or requirement id in refs would move that idea's last touch
                        # (the fold does that for any ref), so it is kept as a tag instead.
                        if (
                            s.state.idea_index(jid) >= 0
                            or s.state.requirement_index(jid) >= 0
                        ):
                            tags.append("from:" + jid)
                        else:
                            journal_refs.append(jid)
                    elif jid.byte_length() > 0:
                        refs_dropped += 1
                elif ek in message_of_event:
                    source_refs.append(
                        prop(nodes[node_at[message_of_event[ek]]], "id")
                    )
            for older in list_of(supersedes, key):
                source_refs.append(prop(nodes[node_at[older]], "id"))
            var source_id = prop(node, "id")
            var conf = node["properties"].get(
                PythonObject("confidence"), Python.none()
            )
            var value: Optional[Float64] = None
            if Bool(conf != Python.none()):
                value = Optional[Float64](Float64(String(conf)))
            pending.append(
                Pending(
                    kind,
                    source_id,
                    when^,
                    prop(node, "content"),
                    value,
                    None,
                    None,
                    None,
                    NEO_PREFIX + source_id,
                    tags^,
                    journal_refs^,
                    source_refs^,
                    String(),
                )
            )

        elif label == "Message":
            var carried_by_test_source = False
            if key in event_of_message:
                carried_by_test_source = is_test_source(
                    prop(nodes[node_at[event_of_message[key]]], "source")
                )
            if (
                is_test_project(prop(node, "projectId"))
                or carried_by_test_source
            ):
                skipped_test += 1
                continue
            var source_id = prop(node, "id")
            var tags = List[String]()
            tags.append(prop(node, "kind"))
            var session_id = prop(node, "sessionId")
            var why: Optional[String] = None
            if session_id.byte_length() > 0:
                why = Optional[String](session_id)
            pending.append(
                Pending(
                    "exchange",
                    source_id,
                    parse_instant(prop(node, "occurredAt")),
                    prop(node, "content"),
                    None,
                    Optional[String](prop(node, "role")),
                    why,
                    None,
                    NEO_PREFIX + source_id,
                    tags^,
                    List[String](),
                    List[String](),
                    String(),
                )
            )

        elif label == "Document":
            var source = prop(node, "source")
            if is_test_source(source) or is_test_project(
                prop(node, "projectId")
            ):
                skipped_test += 1
                continue
            var cut = source.find(READ_MARK)
            if cut < 0 or not source.endswith(")"):
                raise user_error(
                    "document '"
                    + prop(node, "title")
                    + "' has no read date in its source"
                )
            var locator = ascii_prefix(source, cut)
            var date_text = ascii_tail(source, cut + READ_MARK.byte_length())
            var date = ascii_prefix(date_text, date_text.byte_length() - 1)
            # The passages, in ordinal order. An ordinal is a small integer.
            var chunk_keys = list_of(chunks_of, key)
            var ordinals = List[Int]()
            var passages = List[String]()
            for ck in chunk_keys:
                var chunk = nodes[node_at[ck]]
                ordinals.append(Int(String(chunk["properties"]["ordinal"])))
                passages.append(prop(chunk, "content"))
            var by_ordinal = sort_order_by_int(ordinals)
            var body = String()
            for k in range(len(by_ordinal)):
                if k > 0:
                    body += "\n\n"
                body += passages[by_ordinal[k]]
            var hashlib = Python.import_module("hashlib")
            var sha = String(
                hashlib.sha256(PythonObject(body).encode()).hexdigest()
            )
            pending.append(
                Pending(
                    "read",
                    sha,
                    parse_instant(date + "T00:00:00+00:00"),
                    prop(node, "title"),
                    None,
                    None,
                    None,
                    Optional[String](locator),
                    "docs/" + sha + ".md",
                    List[String](),
                    List[String](),
                    List[String](),
                    body,
                )
            )

        elif label == "Event":
            var source = prop(node, "source")
            if source == "vinci-journal":
                journal_copies += 1
            elif key in message_of_event or is_test_source(source):
                pass
            else:
                events_unmapped += 1

        else:
            labels_not_mapped[label] = labels_not_mapped.get(label, 0) + 1

    # What the journal already holds: each record is keyed by its `where`.
    var existing_where = Dict[String, String]()
    var max_number = Dict[String, Int]()
    for e in journal_now:
        if e.id is None:
            continue
        var id = e.id.value()
        if id.byte_length() < 2:
            continue
        var letter = ascii_prefix(id, 1)
        var digits = ascii_tail(id, 1)
        try:
            var number = Int(digits)
            if letter in ["k", "m", "d", "e"] and number > max_number.get(
                letter, 0
            ):
                max_number[letter] = number
        except:
            pass
        if e.where_ is not None and is_imported_kind(e.kind):
            existing_where[e.where_.value()] = id

    var dates = List[Instant]()
    for i in range(len(pending)):
        dates.append(pending[i].at.copy())
    var order = sort_order(dates)

    # First pass: ids in date order, so a ref can name any record of this run.
    var this_run = Dict[String, String]()
    var id_of_source = Dict[String, String]()
    var assigned = List[String]()
    var is_new = List[Bool]()
    var counts_new = Dict[String, Int]()
    var counts_already = Dict[String, Int]()
    for _ in range(len(pending)):
        assigned.append(String())
        is_new.append(False)
    for k in range(len(order)):
        var p = order[k]
        var where_ = pending[p].where_
        if where_ in existing_where:
            assigned[p] = existing_where[where_]
            counts_already[pending[p].kind] = (
                counts_already.get(pending[p].kind, 0) + 1
            )
        elif where_ in this_run:
            assigned[p] = this_run[where_]
            counts_already[pending[p].kind] = (
                counts_already.get(pending[p].kind, 0) + 1
            )
        else:
            var letter = prefix_for(pending[p].kind)
            var number = max_number.get(letter, 0) + 1
            max_number[letter] = number
            assigned[p] = letter + String(number)
            this_run[where_] = assigned[p]
            is_new[p] = True
        if pending[p].kind != "read":
            id_of_source[pending[p].source_id] = assigned[p]

    # Second pass: write the new lines, oldest first.
    var docs_dir = directory_of(s.journal.path) + "/docs"
    for k in range(len(order)):
        var p = order[k]
        if not is_new[p]:
            continue
        var refs = pending[p].journal_refs.copy()
        for source in pending[p].source_refs:
            if source in id_of_source:
                refs.append(id_of_source[source])
        var refs_opt: Optional[List[String]] = None
        if len(refs) > 0:
            refs_opt = Optional[List[String]](refs^)
        var tags_opt: Optional[List[String]] = None
        if len(pending[p].tags) > 0:
            tags_opt = Optional[List[String]](pending[p].tags.copy())
        var event = Event(
            pending[p].at.copy(),
            pending[p].kind,
            Optional[String](assigned[p]),
            Optional[String](pending[p].text),
            refs_opt^,
            pending[p].value.copy(),
            None,
            pending[p].who.copy(),
            pending[p].why.copy(),
            pending[p].url.copy(),
            None,
            tags_opt^,
            Optional[String](pending[p].where_),
        )
        s.record(event)
        counts_new[pending[p].kind] = counts_new.get(pending[p].kind, 0) + 1
        if pending[p].kind == "read":
            makedirs(docs_dir, exist_ok=True)
            var body_path = docs_dir + "/" + pending[p].source_id + ".md"
            if not Path(body_path).exists():
                with open(body_path, "w") as f:
                    f.write(pending[p].body)

    var labels_order = List[String]()
    labels_order.append("Job")
    labels_order.append("Entity")
    labels_order.append("Claim")
    labels_order.append("Procedure")
    labels_order.append("ReasoningTrace")
    labels_order.append("Project")
    labels_order.append("Conversation")
    labels_order.append("Chunk")
    labels_order.append("SchemaMetadata")
    var summary = String('{"imported":{') + counts_json(
        kind_order(), counts_new
    )
    summary += '},"already":{' + counts_json(kind_order(), counts_already)
    summary += '},"skipped":{"test":' + String(skipped_test)
    summary += ',"journal copies":' + String(journal_copies)
    summary += ',"events not mapped":' + String(events_unmapped)
    summary += ',"refs to ids not in the journal":' + String(refs_dropped)
    summary += '},"not mapped":{' + counts_json(labels_order, labels_not_mapped)
    summary += "}}"
    print(summary)


def sort_order_by_int(values: List[Int]) -> List[Int]:
    """Indices of values, smallest first. A stable insertion sort."""
    var order = List[Int]()
    for i in range(len(values)):
        var at = len(order)
        while at > 0 and values[order[at - 1]] > values[i]:
            at -= 1
        order.insert(at, i)
    return order^
