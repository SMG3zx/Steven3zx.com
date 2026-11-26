"""Recall over the index: lexical (FTS5) and vector routes, fused by reciprocal rank, then
abstention, the corrective query and date boosts.

Tokens are the query terms (terms.py). The routes, fusion, and the ordering are in route.py,
fusion.py, vectors.py and dates.py; abstention in abstain.py.

Port of mojo/src/recall.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass, field, replace
from typing import Optional

from .abstain import Abstention, assess_evidence, corrective_query, mentions, should_abstain
from .dates import DateWindow, date_boost, parse_query_dates
from .embed_config import EmbedConfig
from .fusion import Candidate, Weights, content_key, fuse, route_score, weights_for
from .index_store import IndexStore
from .jsonline import ascii_lower
from .route import ROUTE_TEMPORAL, Plan, plan_of, route_query
from .session import user_error
from .terms import fts_query, query_terms
from .vectors import VectorIndex, embed_text_for_query, load_vectors

LEXICAL = "lexical"
VECTOR = "vector"
DEFAULT_ROUTES = "lexical,vector"
MIN_CANDIDATES = 20

JOURNAL_SOURCE = "journal"
GIT_SOURCE = "git"
VAULTS_SOURCE = "vaults"
ACTIVITY_SOURCE = "activity"
# all is the three sources that were always searched together. activity is named on its own.
SOURCES_ALL = "journal,git,vaults"

FEED_SLOTS = 3
# The sources a recall takes from the journal's own pass. Feeds are the rest.
DEFAULT_FEEDS = "git,vaults"


@dataclass
class Recall:
    """What one recall call returns. results are best first and already capped."""

    query: str
    route: str
    complexity: str
    routes: list[str]
    results: list[Candidate]
    abstention: Abstention
    corrected: bool
    correction_query: str
    dates: list[DateWindow]
    label: str
    feed_hint: int


def parse_sources(text: str) -> list[str]:
    """The sources a recall searches: a comma list of journal, git, vaults and activity, or
    all (journal, git and vaults). The journal is the default. An unknown name is an error, so
    a typo is not a silent empty search."""
    if text == "all":
        return parse_sources(SOURCES_ALL)
    out: list[str] = []
    for name in text.split(","):
        if name not in (JOURNAL_SOURCE, GIT_SOURCE, VAULTS_SOURCE, ACTIVITY_SOURCE):
            raise user_error(
                "unknown source '" + name + "' (journal, git, vaults, activity, or all)"
            )
        if name not in out:
            out.append(name)
    return out


def keep_sources(items: list[Candidate], sources: list[str]) -> list[Candidate]:
    """The candidates whose source is one of sources, in their order."""
    return [c for c in items if c.source in sources]


def journal_sources(sources: list[str]) -> list[str]:
    return [n for n in sources if n == JOURNAL_SOURCE]


def feed_sources(sources: list[str]) -> list[str]:
    return [n for n in sources if n != JOURNAL_SOURCE]


def take_unseen(
    out: list[Candidate], seen: list[str], items: list[Candidate], start: int, stop: int, limit: int
) -> None:
    """Append items[start:stop] to out, skipping a text that is already there, until out holds
    limit results."""
    last = min(stop, len(items))
    i = start
    while i < last and len(out) < limit:
        key = content_key(items[i].text)
        if key not in seen:
            seen.append(key)
            out.append(items[i])
        i += 1


def merge_reserved(journal: list[Candidate], feeds: list[Candidate], limit: int) -> list[Candidate]:
    """The journal first, with FEED_SLOTS of the top results given to the feeds.

    The journal's first limit - FEED_SLOTS results keep their places. Then the feeds' first
    FEED_SLOTS, then the rest of the journal, then the rest of the feeds, so a short journal
    list is filled from the feeds. Each list keeps its own order, and a text already in the
    output is not repeated. Feed scores are never compared with journal scores.
    """
    top = max(limit - FEED_SLOTS, 1)
    out: list[Candidate] = []
    seen: list[str] = []
    take_unseen(out, seen, journal, 0, top, limit)
    take_unseen(out, seen, feeds, 0, FEED_SLOTS, limit)
    take_unseen(out, seen, journal, top, len(journal), limit)
    take_unseen(out, seen, feeds, FEED_SLOTS, len(feeds), limit)
    return out


def feed_matches(feeds: list[Candidate], query: str) -> int:
    """How many feed records cover at least half of the question's terms, by the test the
    abstention uses for the journal. A shared word is not enough. Zero when the question has no
    terms."""
    terms = query_terms(query)
    if len(terms) == 0:
        return 0
    matches = 0
    for c in feeds:
        text = ascii_lower(c.text)
        covered = sum(1 for term in terms if mentions(text, term))
        if covered > 0 and covered * 2 >= len(terms):
            matches += 1
    return matches


def feed_hint_line(matches: int) -> str:
    """The line that says feed records may answer a question the journal abstained on."""
    return "no journal record; " + str(matches) + " feed records may match: use sources " + DEFAULT_FEEDS


@dataclass
class Scored:
    """Candidates from one FTS table, best first, with their bm25 scores (lower is better)."""

    candidates: list[Candidate] = field(default_factory=list)
    scores: list[float] = field(default_factory=list)


def lexical_table(
    store: IndexStore, table: str, match_expr: str, limit: int, status_clause: str, sources: list[str]
) -> Scored:
    """One FTS table, filtered to the named sources. The journal's table holds only journal
    facts, and the feeds' table only feed records, so their bm25 scores never see each other."""
    quoted = ",".join("'" + s + "'" for s in sources)
    rows = store.conn.execute(
        "SELECT f.rowid, f.cite, f.kind, f.text, f.at, f.valid_from,"
        " f.source, f.trusted, bm25(" + table + ") FROM " + table
        + " JOIN facts AS f ON f.rowid = " + table + ".rowid WHERE " + table + " MATCH ? AND "
        + status_clause + " AND f.source IN (" + quoted + ") ORDER BY bm25(" + table + ") LIMIT ?",
        (match_expr, limit),
    ).fetchall()
    out = Scored()
    for row in rows:
        c = Candidate(
            int(row[0]),
            str(row[1]),
            str(row[2]),
            str(row[3]),
            str(row[4]),
            str(row[5]),
            str(row[6]),
            str(row[7]) == "1",
        )
        c.in_lexical = True
        out.candidates.append(c)
        out.scores.append(float(row[8]))
    return out


def lexical(store: IndexStore, query: str, limit: int, include_superseded: bool) -> list[Candidate]:
    """The journal's lexical candidates, as before feeds."""
    return lexical_from(store, query, limit, include_superseded, [JOURNAL_SOURCE])


def merge_ranked(first: Scored, second: Scored, limit: int) -> Scored:
    """Two lists, each ranked by its bm25 score (lower is better), merged by score. A tie goes
    to the first list, so the journal stays ahead of the feeds on equal scores."""
    out = Scored()
    i = 0
    j = 0
    while len(out.candidates) < limit and (i < len(first.candidates) or j < len(second.candidates)):
        take_first = j >= len(second.candidates)
        if i < len(first.candidates) and j < len(second.candidates):
            take_first = first.scores[i] <= second.scores[j]
        if take_first:
            out.candidates.append(first.candidates[i])
            out.scores.append(first.scores[i])
            i += 1
        else:
            out.candidates.append(second.candidates[j])
            out.scores.append(second.scores[j])
            j += 1
    return out


def lexical_from(
    store: IndexStore, query: str, limit: int, include_superseded: bool, sources: list[str]
) -> list[Candidate]:
    """Candidates from FTS5, best bm25 first. Current and active versions only, or, for the
    temporal route, superseded and active versions too. The journal, the feeds (git, vaults)
    and the activity feed are three FTS tables, so each table's bm25 scores count only its own
    rows. When more than one table is asked for, their lists are merged by score."""
    match_expr = fts_query(query)
    if match_expr == "" or limit <= 0 or len(sources) == 0:
        return []
    status_clause = "f.valid_until IS NULL AND f.status = 'active'"
    if include_superseded:
        status_clause = "f.status IN ('active', 'superseded')"
    journal = [n for n in sources if n == JOURNAL_SOURCE]
    activity = [n for n in sources if n == ACTIVITY_SOURCE]
    feeds = [n for n in sources if n not in (JOURNAL_SOURCE, ACTIVITY_SOURCE)]
    merged = Scored()
    have_any = False
    if len(journal) > 0:
        merged = lexical_table(store, "fts", match_expr, limit, status_clause, journal)
        have_any = True
    if len(feeds) > 0:
        found = lexical_table(store, "fts_feeds", match_expr, limit, status_clause, feeds)
        merged = merge_ranked(merged, found, limit) if have_any else found
        have_any = True
    if len(activity) > 0:
        found = lexical_table(store, "fts_activity", match_expr, limit, status_clause, activity)
        merged = merge_ranked(merged, found, limit) if have_any else found
        have_any = True
    return list(merged.candidates)


@dataclass
class Settings:
    """The options one recall call runs with, apart from the question itself.

    now_utc is the clock in ticks (Instant.utc) and today its local date, yyyy-mm-dd. Both come
    from the command, so the tests can pin them.
    """

    limit: int
    routes: list[str]
    now_utc: int
    today: str
    embed: EmbedConfig
    # The journal only, as before the feeds. Other sources are named explicitly.
    sources: list[str] = field(default_factory=lambda: [JOURNAL_SOURCE])
    # Eval only (--weights, VINCI_RECALL_WEIGHTS): replaces the per-route fusion weights for
    # every route, or for route semantic alone with scope=semantic. None is the default.
    fusion: Optional[Weights] = None


def check_routes(routes: list[str]) -> None:
    if len(routes) == 0:
        raise ValueError("recall: no route given")
    for r in routes:
        if r not in (LEXICAL, VECTOR):
            raise ValueError("recall: unknown route " + r + " (lexical, vector)")


def order_by(items: list[Candidate], keys: list[float]) -> list[Candidate]:
    """Stable sort of candidates by a parallel list of keys, descending."""
    order = sorted(range(len(items)), key=lambda i: keys[i], reverse=True)
    return [items[i] for i in order]


def one_pass(
    store: IndexStore,
    query: str,
    route: str,
    plan: Plan,
    windows: list[DateWindow],
    settings: Settings,
    index: VectorIndex,
) -> list[Candidate]:
    """One lookup: the routes, fusion, the route ordering, and the content dedupe.

    The cut is at limit * 4 before the dedupe and at limit after it.
    """
    limit = settings.limit
    candidates_limit = max(limit * plan.candidate_multiplier, MIN_CANDIDATES)
    temporal = route == "temporal"
    lexical_hits: list[Candidate] = []
    if LEXICAL in settings.routes:
        lexical_hits = lexical_from(store, query, candidates_limit, temporal, settings.sources)
    vector_hits: list[Candidate] = []
    query_vector: list[float] = []
    use_vectors = VECTOR in settings.routes and index.count() > 0
    if use_vectors:
        query_vector = embed_text_for_query(settings.embed, query)
        vector_hits = index.search(query_vector, candidates_limit)
    weights = weights_for(route)
    if settings.fusion is not None:
        override = settings.fusion
        if not override.semantic_only or route == "semantic":
            weights = override
    fused = keep_sources(fuse(lexical_hits, vector_hits, weights), settings.sources)
    if use_vectors:
        for c in fused:
            if not c.has_cosine:
                cosine = index.cosine_of(c.rowid, query_vector)
                if cosine is not None:
                    c.has_cosine = True
                    c.cosine = cosine
    peak = 0.0
    for c in fused:
        if c.fused > peak:
            peak = c.fused
    if peak <= 0:
        peak = 1.0
    now_utc = settings.now_utc
    keys = [route_score(c, route, now_utc, peak, date_boost(windows, c.valid_from)) for c in fused]
    ordered = order_by(fused, keys)
    cut = limit * 4
    seen: list[str] = []
    out: list[Candidate] = []
    for i, c in enumerate(ordered):
        if len(out) >= limit or i >= cut:
            break
        key = content_key(c.text)
        if key in seen:
            continue
        seen.append(key)
        out.append(c)
    return out


def merge_corrected(first: list[Candidate], second: list[Candidate], limit: int) -> list[Candidate]:
    """The better-scored copy of each fact, in first-seen order, capped at limit."""
    keys: list[int] = []
    items: list[Candidate] = []
    for source in (first, second):
        for c in source:
            if c.rowid not in keys:
                keys.append(c.rowid)
                items.append(c)
            else:
                at = keys.index(c.rowid)
                if c.fused > items[at].fused:
                    items[at] = c
    return items[:limit]


def recall(store: IndexStore, query: str, settings: Settings) -> Recall:
    """Retrieval and the answer rules, as Service.Retrieve does them.

    1. Route the question (temporal, procedural, graph or semantic) and plan it.
    2. Run the chosen routes and fuse them, then order by RouteScore.
    3. If the best fused score is under 0.25 (or there is nothing), try once more with the
       question's distinct terms, and keep the better copy of each fact.
    4. Abstain when the results do not cover the question (ShouldAbstain).
    """
    check_routes(settings.routes)
    route = route_query(query)
    plan = plan_of(query, route)
    windows = parse_query_dates(query, settings.today)
    index = VectorIndex(settings.embed.label)
    if VECTOR in settings.routes:
        index = load_vectors(store, settings.embed.label, route == ROUTE_TEMPORAL)
    # The journal's pass is the answer's pass: its correction, its abstention and its results
    # come from the journal alone, so no feed text can change them. With no journal in the
    # sources, the pass is the feeds' own, as before.
    journal = journal_sources(settings.sources)
    feeds = feed_sources(settings.sources)
    has_journal = len(journal) > 0
    first = replace(settings, sources=list(journal) if has_journal else list(settings.sources))
    results = one_pass(store, query, route, plan, windows, first, index)
    corrected = False
    second_query = ""
    if assess_evidence(results).needs_correction:
        second_query = corrective_query(query)
        if second_query != "":
            again = one_pass(store, second_query, route, plan, windows, first, index)
            results = merge_corrected(results, again, settings.limit)
            corrected = True
    abstention = should_abstain(results, query)
    feed_hint = 0
    if has_journal and len(feeds) > 0:
        # The journal first, with the feeds' share after it (merge_reserved).
        feed_settings = replace(settings, sources=list(feeds))
        feed_results = one_pass(store, query, route, plan, windows, feed_settings, index)
        results = merge_reserved(results, feed_results, settings.limit)
    elif has_journal and abstention.abstain:
        # The default (journal only): the journal abstained, so the feeds are not shown. A hint
        # says whether feed records may answer the question.
        hint_settings = replace(settings, sources=DEFAULT_FEEDS.split(","))
        hint_results = one_pass(store, query, route, plan, windows, hint_settings, index)
        feed_hint = feed_matches(hint_results, query)
    return Recall(
        query,
        route,
        plan.complexity,
        list(settings.routes),
        results,
        abstention,
        corrected,
        second_query,
        windows,
        settings.embed.label,
        feed_hint,
    )
