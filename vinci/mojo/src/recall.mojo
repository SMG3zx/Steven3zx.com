# Recall over the index (M3 step 2b): lexical (FTS5) and vector routes, fused by
# reciprocal rank, then abstention, the corrective query and date boosts, as the C#
# recall in src/Memory/Service.cs and src/Memory/Neo4jStore.cs does them.
#
# Tokens are Retrieval.Terms (below). The routes, fusion, and the ordering are in
# route.mojo, fusion.mojo, vectors.mojo and dates.mojo; abstention in abstain.mojo.

from std.python import Python, PythonObject

from abstain import (
    Abstention,
    assess_evidence,
    corrective_query,
    should_abstain,
)
from dates import DateWindow, date_boost, parse_query_dates
from embed_edge import EmbedConfig
from fusion import Candidate, content_key, fuse, route_score, weights_for
from index_store import IndexStore, py_params
from jsonline import ascii_lower
from route import Plan, plan_of, route_query
from vectors import VectorIndex, embed_text_for_query, load_vectors

comptime LEXICAL = "lexical"
comptime VECTOR = "vector"
comptime DEFAULT_ROUTES = "lexical,vector"
comptime MIN_CANDIDATES = 20

# Retrieval.cs StopWords, in its order. A term in this list is not searched for.
comptime STOP_WORDS = (
    "a an and are about after again all any at be been before between by can"
    " did do does during for from had has have how i if in into is it its me"
    " my no not of on or our out over so some than that the their then there"
    " these this those to under up used we what when where which while who why"
    " with your also"
)


@fieldwise_init
struct Recall(Movable):
    """What one recall call returns. results are best first and already capped.
    """

    var query: String
    var route: String
    var complexity: String
    var routes: List[String]
    var results: List[Candidate]
    var abstention: Abstention
    var corrected: Bool
    var correction_query: String
    var dates: List[DateWindow]
    var label: String


def is_term_byte(b: UInt8) -> Bool:
    """Bytes allowed after the first byte of a term: a-z, 0-9, underscore, hyphen.
    """
    var c = Int(b)
    return (c >= 97 and c <= 122) or (c >= 48 and c <= 57) or c == 95 or c == 45


def is_term_start(b: UInt8) -> Bool:
    """Bytes that can start a term: a-z and 0-9."""
    var c = Int(b)
    return (c >= 97 and c <= 122) or (c >= 48 and c <= 57)


def is_stop_word(term: String) -> Bool:
    for word in String(STOP_WORDS).split(" "):
        if String(word) == term:
            return True
    return False


def query_terms(query: String) -> List[String]:
    """Retrieval.Terms: lowercase, then runs [a-z0-9][a-z0-9_-]{2,}, minus stop words.

    Runs shorter than three bytes are dropped, as the regular expression drops them.
    Non-ASCII letters are not terms, as in the C# pattern.
    """
    var out = List[String]()
    var src = ascii_lower(query).as_bytes()
    var n = len(src)
    var i = 0
    while i < n:
        if not is_term_start(src[i]):
            i += 1
            continue
        var j = i + 1
        while j < n and is_term_byte(src[j]):
            j += 1
        if j - i < 3:
            i += 1
            continue
        var term = List[UInt8]()
        for k in range(i, j):
            term.append(src[k])
        var word = String(unsafe_from_utf8=term)
        if not is_stop_word(word):
            out.append(word)
        i = j
    return out^


def quote_phrase(word: String) raises -> String:
    """One FTS5 phrase. A double quote inside the word is doubled, so no input is syntax.
    """
    return '"' + String(word.replace('"', '""')) + '"'


def fts_query(query: String) raises -> String:
    """The FTS5 MATCH expression for a question: its terms, quoted and joined with OR.

    Neo4j's full-text index joins a Lucene query's words with OR by default, and
    the C# route passes its terms on that way, so OR is kept. When no term is left
    (only stop words, or no letters), the question's own words are quoted instead.
    Empty when there is nothing to search for.
    """
    var terms = query_terms(query)
    var parts = List[String]()
    if len(terms) > 0:
        for term in terms:
            parts.append(quote_phrase(term))
    else:
        for word in query.split(" "):
            var w = String(word)
            if w.byte_length() > 0:
                parts.append(quote_phrase(w))
    var out = String()
    for i in range(len(parts)):
        if i > 0:
            out += " OR "
        out += parts[i]
    return out


def lexical(
    store: IndexStore,
    query: String,
    limit: Int,
    include_superseded: Bool,
) raises -> List[Candidate]:
    """Candidates from FTS5, best bm25 first. Current and active versions only, or,
    for the temporal route, superseded and active versions too."""
    var out = List[Candidate]()
    var match_expr = fts_query(query)
    if match_expr.byte_length() == 0 or limit <= 0:
        return out^
    var status_clause = "f.valid_until IS NULL AND f.status = 'active'"
    if include_superseded:
        status_clause = "f.status IN ('active', 'superseded')"
    var rows = store.conn.execute(
        (
            "SELECT f.rowid, f.cite, f.kind, f.text, f.at, f.valid_from,"
            " f.source, f.trusted FROM fts JOIN facts AS f ON f.rowid ="
            " fts.rowid WHERE fts MATCH ? AND "
            + status_clause
            + " ORDER BY bm25(fts) LIMIT ?"
        ),
        py_params(PythonObject(match_expr), PythonObject(limit)),
    ).fetchall()
    for row in rows:
        var c = Candidate(
            Int(String(row[0])),
            String(row[1]),
            String(row[2]),
            String(row[3]),
            String(row[4]),
            String(row[5]),
            String(row[6]),
            String(row[7]) == "1",
        )
        c.in_lexical = True
        out.append(c^)
    return out^


struct Settings(Copyable, Movable):
    """The options one recall call runs with, apart from the question itself.

    now_utc is the clock in ticks (Instant.utc) and today its local date, yyyy-mm-dd.
    Both come from the command, so the tests can pin them.
    """

    var limit: Int
    var routes: List[String]
    var now_utc: Int
    var today: String
    var embed: EmbedConfig

    def __init__(
        out self,
        limit: Int,
        var routes: List[String],
        now_utc: Int,
        var today: String,
        var embed: EmbedConfig,
    ):
        self.limit = limit
        self.routes = routes^
        self.now_utc = now_utc
        self.today = today^
        self.embed = embed^


def has_route(routes: List[String], name: String) -> Bool:
    for r in routes:
        if r == name:
            return True
    return False


def check_routes(routes: List[String]) raises:
    if len(routes) == 0:
        raise Error("recall: no route given")
    for r in routes:
        if r != LEXICAL and r != VECTOR:
            raise Error("recall: unknown route " + r + " (lexical, vector)")


def order_by(
    var items: List[Candidate], var keys: List[Float64]
) -> List[Candidate]:
    """Stable insertion sort of candidates by a parallel list of keys, descending.
    """
    for i in range(1, len(items)):
        var j = i
        while j > 0 and keys[j - 1] < keys[j]:
            items.swap_elements(j - 1, j)
            keys.swap_elements(j - 1, j)
            j -= 1
    return items^


def one_pass(
    store: IndexStore,
    query: String,
    route: String,
    plan: Plan,
    windows: List[DateWindow],
    settings: Settings,
    index: VectorIndex,
) raises -> List[Candidate]:
    """One lookup: the routes, fusion, the route ordering, and the content dedupe.

    This is Neo4jStore.Recall for one question. The cut is at limit * 4 before the
    dedupe and at limit after it, as in the C#.
    """
    var limit = settings.limit
    var candidates_limit = limit * plan.candidate_multiplier
    if candidates_limit < MIN_CANDIDATES:
        candidates_limit = MIN_CANDIDATES
    var temporal = route == "temporal"
    var lexical_hits = List[Candidate]()
    if has_route(settings.routes, LEXICAL):
        lexical_hits = lexical(store, query, candidates_limit, temporal)
    var vector_hits = List[Candidate]()
    var query_vector = List[Float32]()
    var use_vectors = has_route(settings.routes, VECTOR) and index.count() > 0
    if use_vectors:
        query_vector = embed_text_for_query(settings.embed, query)
        vector_hits = index.search(query_vector, candidates_limit)
    var fused = fuse(lexical_hits, vector_hits, weights_for(route))
    if use_vectors:
        for i in range(len(fused)):
            if not fused[i].has_cosine:
                var cosine = index.cosine_of(fused[i].rowid, query_vector)
                if cosine:
                    fused[i].has_cosine = True
                    fused[i].cosine = cosine.value()
    var peak: Float64 = 0
    for c in fused:
        if c.fused > peak:
            peak = c.fused
    if peak <= 0:
        peak = 1
    var now_utc = settings.now_utc
    var keys = List[Float64]()
    for c in fused:
        keys.append(
            route_score(
                c,
                route,
                now_utc,
                peak,
                date_boost(windows, c.valid_from),
            )
        )
    var ordered = order_by(fused^, keys^)
    var cut = limit * 4
    var seen = List[String]()
    var out = List[Candidate]()
    for i in range(len(ordered)):
        if len(out) >= limit or i >= cut:
            break
        var key = content_key(ordered[i].text)
        var duplicate = False
        for s in seen:
            if s == key:
                duplicate = True
                break
        if duplicate:
            continue
        seen.append(key)
        out.append(ordered[i].copy())
    return out^


def merge_corrected(
    first: List[Candidate], second: List[Candidate], limit: Int
) -> List[Candidate]:
    """The better-scored copy of each fact, in first-seen order, capped at limit.
    """
    var keys = List[Int]()
    var items = List[Candidate]()
    for source in [first.copy(), second.copy()]:
        for c in source:
            var at = -1
            for k in range(len(keys)):
                if keys[k] == c.rowid:
                    at = k
                    break
            if at < 0:
                keys.append(c.rowid)
                items.append(c.copy())
            elif c.fused > items[at].fused:
                items[at] = c.copy()
    var out = List[Candidate]()
    for i in range(len(items)):
        if len(out) >= limit:
            break
        out.append(items[i].copy())
    return out^


def recall(
    store: IndexStore,
    query: String,
    settings: Settings,
) raises -> Recall:
    """Retrieval and the answer rules, as Service.Retrieve does them.

    1. Route the question (temporal, procedural, graph or semantic) and plan it.
    2. Run the chosen routes and fuse them, then order by RouteScore.
    3. If the best fused score is under 0.25 (or there is nothing), try once more with
       the question's distinct terms, and keep the better copy of each fact.
    4. Abstain when the results do not cover the question (ShouldAbstain).
    """
    check_routes(settings.routes)
    var route = route_query(query)
    var plan = plan_of(query, route)
    var windows = parse_query_dates(query, settings.today)
    var index = VectorIndex(settings.embed.label)
    if has_route(settings.routes, VECTOR):
        index = load_vectors(store, settings.embed.label, route == "temporal")
    var results = one_pass(store, query, route, plan, windows, settings, index)
    var corrected = False
    var second_query = String()
    if assess_evidence(results).needs_correction:
        second_query = corrective_query(query)
        if second_query.byte_length() > 0:
            var again = one_pass(
                store, second_query, route, plan, windows, settings, index
            )
            results = merge_corrected(results, again, settings.limit)
            corrected = True
    var abstention = should_abstain(results, query)
    return Recall(
        query,
        route,
        plan.complexity,
        settings.routes.copy(),
        results^,
        abstention^,
        corrected,
        second_query,
        windows^,
        settings.embed.label,
    )
