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
    mentions,
    should_abstain,
)
from dates import DateWindow, date_boost, parse_query_dates
from embed_edge import EmbedConfig
from fusion import (
    Candidate,
    Weights,
    content_key,
    fuse,
    route_score,
    weights_for,
)
from index_store import IndexStore, py_params
from jsonline import ascii_lower
from route import Plan, plan_of, route_query
from session import user_error
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
    var feed_hint: Int


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


comptime JOURNAL_SOURCE = "journal"
comptime GIT_SOURCE = "git"
comptime VAULTS_SOURCE = "vaults"
comptime ACTIVITY_SOURCE = "activity"
# all is the three sources that were always searched together. activity is named on its own.
comptime SOURCES_ALL = "journal,git,vaults"


def parse_sources(text: String) raises -> List[String]:
    """The sources a recall searches: a comma list of journal, git, vaults and activity, or
    all (journal, git and vaults). The journal is the default. An unknown name is an error,
    so a typo is not a silent empty search."""
    var out = List[String]()
    if text == "all":
        return parse_sources(String(SOURCES_ALL))
    for raw in text.split(","):
        var name = String(raw)
        if (
            name != JOURNAL_SOURCE
            and name != GIT_SOURCE
            and name != VAULTS_SOURCE
            and name != ACTIVITY_SOURCE
        ):
            raise user_error(
                "unknown source '"
                + name
                + "' (journal, git, vaults, activity, or all)"
            )
        if name not in out:
            out.append(name)
    return out^


def source_in(sources: List[String], source: String) -> Bool:
    for s in sources:
        if s == source:
            return True
    return False


def keep_sources(
    items: List[Candidate], sources: List[String]
) -> List[Candidate]:
    """The candidates whose source is one of sources, in their order."""
    var out = List[Candidate]()
    for c in items:
        if source_in(sources, c.source):
            out.append(c.copy())
    return out^


comptime FEED_SLOTS = 3
# The sources a recall takes from the journal's own pass. Feeds are the rest.
comptime DEFAULT_FEEDS = "git,vaults"


def journal_sources(sources: List[String]) -> List[String]:
    var out = List[String]()
    for name in sources:
        if name == JOURNAL_SOURCE:
            out.append(name.copy())
    return out^


def feed_sources(sources: List[String]) -> List[String]:
    var out = List[String]()
    for name in sources:
        if name != JOURNAL_SOURCE:
            out.append(name.copy())
    return out^


def take_unseen(
    mut out: List[Candidate],
    mut seen: List[String],
    items: List[Candidate],
    start: Int,
    stop: Int,
    limit: Int,
):
    """Append items[start:stop] to out, skipping a text that is already there, until
    out holds limit results."""
    var i = start
    var last = stop
    if last > len(items):
        last = len(items)
    while i < last and len(out) < limit:
        var key = content_key(items[i].text)
        var duplicate = False
        for s in seen:
            if s == key:
                duplicate = True
                break
        if not duplicate:
            seen.append(key)
            out.append(items[i].copy())
        i += 1


def merge_reserved(
    journal: List[Candidate], feeds: List[Candidate], limit: Int
) -> List[Candidate]:
    """The journal first, with FEED_SLOTS of the top results given to the feeds.

    The journal's first limit - FEED_SLOTS results keep their places. Then the feeds'
    first FEED_SLOTS, then the rest of the journal, then the rest of the feeds, so a short
    journal list is filled from the feeds. Each list keeps its own order, and a text
    already in the output is not repeated. Feed scores are never compared with journal
    scores: their bm25 tables are not on one scale (docs/feeds.md, the merge).
    """
    var top = limit - FEED_SLOTS
    if top < 1:
        top = 1
    var out = List[Candidate]()
    var seen = List[String]()
    take_unseen(out, seen, journal, 0, top, limit)
    take_unseen(out, seen, feeds, 0, FEED_SLOTS, limit)
    take_unseen(out, seen, journal, top, len(journal), limit)
    take_unseen(out, seen, feeds, FEED_SLOTS, len(feeds), limit)
    return out^


def feed_matches(feeds: List[Candidate], query: String) -> Int:
    """How many feed records cover at least half of the question's terms, by the test
    the abstention uses for the journal. A shared word is not enough. Zero when the
    question has no terms."""
    var terms = query_terms(query)
    if len(terms) == 0:
        return 0
    var matches = 0
    for c in feeds:
        var text = ascii_lower(c.text)
        var covered = 0
        for term in terms:
            if mentions(text, term):
                covered += 1
        if covered > 0 and covered * 2 >= len(terms):
            matches += 1
    return matches


def feed_hint_line(matches: Int) -> String:
    """The line that says feed records may answer a question the journal abstained on.
    """
    return (
        "no journal record; "
        + String(matches)
        + " feed records may match: use sources "
        + DEFAULT_FEEDS
    )


@fieldwise_init
struct Scored(Movable):
    """Candidates from one FTS table, best first, with their bm25 scores (lower is better).
    """

    var candidates: List[Candidate]
    var scores: List[Float64]


def lexical_table(
    store: IndexStore,
    table: String,
    match_expr: String,
    limit: Int,
    status_clause: String,
    sources: List[String],
) raises -> Scored:
    """One FTS table, filtered to the named sources. The journal's table holds only journal
    facts, and the feeds' table only feed records, so their bm25 scores never see each other.
    """
    var quoted = String()
    for i in range(len(sources)):
        if i > 0:
            quoted += ","
        quoted += "'" + sources[i] + "'"
    var rows = store.conn.execute(
        (
            "SELECT f.rowid, f.cite, f.kind, f.text, f.at, f.valid_from,"
            " f.source, f.trusted, bm25("
            + table
            + ") FROM "
            + table
            + " JOIN facts AS f ON f.rowid = "
            + table
            + ".rowid WHERE "
            + table
            + " MATCH ? AND "
            + status_clause
            + " AND f.source IN ("
            + quoted
            + ") ORDER BY bm25("
            + table
            + ") LIMIT ?"
        ),
        py_params(PythonObject(match_expr), PythonObject(limit)),
    ).fetchall()
    var out = Scored(List[Candidate](), List[Float64]())
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
        out.candidates.append(c^)
        out.scores.append(Float64(String(row[8])))
    return out^


def lexical(
    store: IndexStore,
    query: String,
    limit: Int,
    include_superseded: Bool,
) raises -> List[Candidate]:
    """The journal's lexical candidates, as before feeds (see lexical_from)."""
    var journal = List[String]()
    journal.append(JOURNAL_SOURCE)
    return lexical_from(store, query, limit, include_superseded, journal)


def merge_ranked(first: Scored, second: Scored, limit: Int) -> Scored:
    """Two lists, each ranked by its bm25 score (lower is better), merged by score. A tie
    goes to the first list, so the journal stays ahead of the feeds on equal scores.
    """
    var out = Scored(List[Candidate](), List[Float64]())
    var i = 0
    var j = 0
    while len(out.candidates) < limit and (
        i < len(first.candidates) or j < len(second.candidates)
    ):
        var take_first = j >= len(second.candidates)
        if i < len(first.candidates) and j < len(second.candidates):
            take_first = first.scores[i] <= second.scores[j]
        if take_first:
            out.candidates.append(first.candidates[i].copy())
            out.scores.append(first.scores[i])
            i += 1
        else:
            out.candidates.append(second.candidates[j].copy())
            out.scores.append(second.scores[j])
            j += 1
    return out^


def lexical_from(
    store: IndexStore,
    query: String,
    limit: Int,
    include_superseded: Bool,
    sources: List[String],
) raises -> List[Candidate]:
    """Candidates from FTS5, best bm25 first. Current and active versions only, or,
    for the temporal route, superseded and active versions too.
    The journal, the feeds (git, vaults) and the activity feed are three FTS tables
    (index_store.mojo), so each table's bm25 scores count only its own rows. When more than
    one table is asked for, their lists are merged by score. The source names are checked by
    parse_sources, so they are safe in SQL.
    """
    var out = List[Candidate]()
    var match_expr = fts_query(query)
    if match_expr.byte_length() == 0 or limit <= 0 or len(sources) == 0:
        return out^
    var status_clause = "f.valid_until IS NULL AND f.status = 'active'"
    if include_superseded:
        status_clause = "f.status IN ('active', 'superseded')"
    var journal = List[String]()
    var feeds = List[String]()
    var activity = List[String]()
    for name in sources:
        if name == JOURNAL_SOURCE:
            journal.append(name.copy())
        elif name == ACTIVITY_SOURCE:
            activity.append(name.copy())
        else:
            feeds.append(name.copy())
    var merged = Scored(List[Candidate](), List[Float64]())
    var have_any = False
    if len(journal) > 0:
        merged = lexical_table(
            store, "fts", match_expr, limit, status_clause, journal
        )
        have_any = True
    if len(feeds) > 0:
        var found = lexical_table(
            store, "fts_feeds", match_expr, limit, status_clause, feeds
        )
        merged = merge_ranked(merged, found, limit) if have_any else found^
        have_any = True
    if len(activity) > 0:
        var found = lexical_table(
            store, "fts_activity", match_expr, limit, status_clause, activity
        )
        merged = merge_ranked(merged, found, limit) if have_any else found^
        have_any = True
    return merged.candidates.copy()


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
    var sources: List[String]
    # Eval only (--weights, VINCI_RECALL_WEIGHTS): replaces the per-route fusion weights
    # for every route, or for route semantic alone with scope=semantic. None is the
    # default, the route's own weights.
    var fusion: Optional[Weights]

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
        # The journal only, as before the feeds. Other sources are named explicitly.
        self.sources = List[String]()
        self.sources.append(JOURNAL_SOURCE)
        self.fusion = None


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
        lexical_hits = lexical_from(
            store, query, candidates_limit, temporal, settings.sources
        )
    var vector_hits = List[Candidate]()
    var query_vector = List[Float32]()
    var use_vectors = has_route(settings.routes, VECTOR) and index.count() > 0
    if use_vectors:
        query_vector = embed_text_for_query(settings.embed, query)
        vector_hits = index.search(query_vector, candidates_limit)
    var weights = weights_for(route)
    if settings.fusion:
        var override = settings.fusion.value().copy()
        if not override.semantic_only or route == "semantic":
            weights = override^
    var fused = keep_sources(
        fuse(lexical_hits, vector_hits, weights), settings.sources
    )
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
    # The journal's pass is the answer's pass: its correction, its abstention and its
    # results come from the journal alone, so no feed text can change them (M5c). With no
    # journal in the sources, the pass is the feeds' own, as before.
    var journal = journal_sources(settings.sources)
    var feeds = feed_sources(settings.sources)
    var has_journal = len(journal) > 0
    var first = settings.copy()
    if has_journal:
        first.sources = journal.copy()
    var results = one_pass(store, query, route, plan, windows, first, index)
    var corrected = False
    var second_query = String()
    if assess_evidence(results).needs_correction:
        second_query = corrective_query(query)
        if second_query.byte_length() > 0:
            var again = one_pass(
                store, second_query, route, plan, windows, first, index
            )
            results = merge_corrected(results, again, settings.limit)
            corrected = True
    var abstention = should_abstain(results, query)
    var feed_hint = 0
    if has_journal and len(feeds) > 0:
        # The journal first, with the feeds' share after it (merge_reserved).
        var feed_settings = settings.copy()
        feed_settings.sources = feeds.copy()
        var feed_results = one_pass(
            store, query, route, plan, windows, feed_settings, index
        )
        results = merge_reserved(results, feed_results, settings.limit)
    elif has_journal and abstention.abstain:
        # The default (journal only): the journal abstained, so the feeds are not shown.
        # A hint says whether feed records may answer the question.
        var hint_settings = settings.copy()
        hint_settings.sources = List[String]()
        for name in DEFAULT_FEEDS.split(","):
            hint_settings.sources.append(String(name))
        var hint_results = one_pass(
            store, query, route, plan, windows, hint_settings, index
        )
        feed_hint = feed_matches(hint_results, query)
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
        feed_hint,
    )
