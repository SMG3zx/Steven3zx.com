# Reciprocal-rank fusion, the final ordering, and the content dedupe. Raw scores (bm25, cosine) are not comparable across routes, so
# each route votes by rank: a candidate at rank i (0-based) in route r adds
# weight_r / (k + i + 1), with k = RRF_K = 60 unless an eval sets another. The sum is the
# fused score.

from std.math import pow
from std.python import Python, PythonObject

from route import ROUTE_GRAPH, ROUTE_PROCEDURAL, ROUTE_SEMANTIC, ROUTE_TEMPORAL
from session import user_error
from timeutil import TICKS_PER_DAY, parse_instant

comptime RRF_K = 60
comptime DEFAULT_SEMANTIC = "0.25,1.0,0.2"
comptime DEFAULT_HALF_LIFE_DAYS = 90.0
comptime ROUTE_BOOST = 0.1
comptime RETENTION_WEIGHT = 0.025
comptime RETENTION_WEIGHT_TEMPORAL = 0.08
comptime DATE_BOOST = 0.1


struct Candidate(Copyable, Movable):
    """One fact as recall returns it. The routes fill the flags, fusion the score.

    rowid is the fact version (the key the routes agree on). id is the citation
    (the journal id), kind the journal kind. at and valid_from are journal times.
    """

    var rowid: Int
    var id: String
    var kind: String
    var text: String
    var at: String
    var valid_from: String
    var source: String
    var trusted: Bool
    var in_lexical: Bool
    var in_vector: Bool
    var fused: Float64
    var has_cosine: Bool
    var cosine: Float64

    def __init__(
        out self,
        rowid: Int,
        var id: String,
        var kind: String,
        var text: String,
        var at: String,
        var valid_from: String,
        var source: String,
        trusted: Bool,
    ):
        self.rowid = rowid
        self.id = id^
        self.kind = kind^
        self.text = text^
        self.at = at^
        self.valid_from = valid_from^
        self.source = source^
        self.trusted = trusted
        self.in_lexical = False
        self.in_vector = False
        self.fused = 0.0
        self.has_cosine = False
        self.cosine = 0.0


struct Weights(Copyable, Movable):
    """Retrieval's RankFusion.Weights. graph is kept for the table; no graph route runs.

    rrf_k is the reciprocal-rank constant (RRF_K unless an eval sets another).
    semantic_only limits an eval override to route semantic (scope=semantic).
    """

    var lexical: Float64
    var vector: Float64
    var graph: Float64
    var rrf_k: Int
    var semantic_only: Bool

    def __init__(
        out self,
        lexical: Float64,
        vector: Float64,
        graph: Float64,
        rrf_k: Int = RRF_K,
        semantic_only: Bool = False,
    ):
        self.lexical = lexical
        self.vector = vector
        self.graph = graph
        self.rrf_k = rrf_k
        self.semantic_only = semantic_only

    def of(self, method: String) -> Float64:
        if method == "lexical":
            return self.lexical
        if method == "vector":
            return self.vector
        return self.graph


def semantic_weights() raises -> Weights:
    """The weights for route semantic: FUSION_SEMANTIC "lexical,vector,graph" when it
    is three numbers, else 0.25, 1.0, 0.2. The first version had 0.4, 0.4, 0.2 (equal weights);
    M5 step d measured the lexical share down to 0.25 (docs/memory.md, Fusion weights).
    """
    var os = Python.import_module("os")
    var raw = String(
        os.environ.get(PythonObject("FUSION_SEMANTIC"), DEFAULT_SEMANTIC)
    )
    var parts = List[String]()
    for part in raw.split(","):
        parts.append(String(part))
    if len(parts) == 3:
        try:
            return Weights(
                Float64(parts[0]), Float64(parts[1]), Float64(parts[2])
            )
        except:
            pass
    return Weights(0.25, 1.0, 0.2)


def parse_weights(text: String) raises -> Weights:
    """The eval-only fusion override: "lexical=W,vector=W" and optionally ",k=K", from
    the recall flag --weights or the environment variable VINCI_RECALL_WEIGHTS.

    Both weights are needed, neither may be negative, and their sum must be above zero.
    Only their ratio matters to the ordering, since route scores divide by the peak. k is
    the RRF constant (RRF_K when absent) and must be at least 1. Anything else is an error,
    so a typo is not a silent default.
    """
    var lexical = Float64(-1)
    var vector = Float64(-1)
    var rrf_k = RRF_K
    var semantic_only = False
    for raw in text.split(","):
        var part = String(raw)
        var pair = List[String]()
        for piece in part.split("="):
            pair.append(String(piece))
        if len(pair) != 2:
            raise user_error(
                "weights: each item is name=value, got '" + part + "'"
            )
        var name = pair[0]
        var value_text = pair[1]
        if name == "k":
            try:
                rrf_k = Int(value_text)
            except:
                raise user_error("weights: k must be a whole number")
            if rrf_k < 1:
                raise user_error("weights: k must be at least 1")
        elif name == "scope":
            if value_text == "semantic":
                semantic_only = True
            elif value_text != "all":
                raise user_error("weights: scope is all or semantic")
        elif name == "lexical" or name == "vector":
            var value: Float64
            try:
                value = Float64(value_text)
            except:
                raise user_error("weights: " + name + " must be a number")
            if value < 0:
                raise user_error("weights: " + name + " must not be negative")
            if name == "lexical":
                lexical = value
            else:
                vector = value
        else:
            raise user_error(
                "weights: unknown name '"
                + name
                + "' (lexical, vector, k, scope)"
            )
    if lexical < 0 or vector < 0:
        raise user_error("weights: give both lexical=W and vector=W")
    if lexical + vector <= 0:
        raise user_error("weights: lexical and vector cannot both be zero")
    return Weights(lexical, vector, 0.0, rrf_k, semantic_only)


def weights_for(route: String) raises -> Weights:
    """RankFusion.Weights.For: graph, temporal and procedural have their own mixes.
    """
    if route == ROUTE_GRAPH:
        return Weights(0.25, 0.2, 0.55)
    if route == ROUTE_TEMPORAL:
        return Weights(0.45, 0.2, 0.35)
    if route == ROUTE_PROCEDURAL:
        return Weights(0.5, 0.2, 0.3)
    return semantic_weights()


def fuse(
    lexical: List[Candidate], vector: List[Candidate], weights: Weights
) raises -> List[Candidate]:
    """RankFusion.Fuse over two ranked lists (each already best first).

    A fact found by both routes is one candidate, with both flags and the summed
    score. The output is ordered by fused score, best first; ties keep the order
    the candidates were first seen in (lexical, then vector), as a stable sort does.
    """
    var out = List[Candidate]()
    var position = Dict[Int, Int]()
    for leg in range(2):
        var method = String("lexical")
        var ranked = lexical.copy()
        if leg == 1:
            method = "vector"
            ranked = vector.copy()
        var contribution_weight = weights.of(method)
        for i in range(len(ranked)):
            var contribution = contribution_weight / Float64(
                weights.rrf_k + i + 1
            )
            var rowid = ranked[i].rowid
            if rowid not in position:
                var first = ranked[i].copy()
                first.in_lexical = leg == 0
                first.in_vector = leg == 1
                first.fused = contribution
                position[rowid] = len(out)
                out.append(first^)
            else:
                var at = position[rowid]
                if leg == 0:
                    out[at].in_lexical = True
                else:
                    out[at].in_vector = True
                    out[at].has_cosine = ranked[i].has_cosine
                    out[at].cosine = ranked[i].cosine
                out[at].fused += contribution
    return stable_sort_by_fused(out^)


def stable_sort_by_fused(var items: List[Candidate]) -> List[Candidate]:
    """Insertion sort by fused score, descending. Stable, and the lists are short.
    """
    for i in range(1, len(items)):
        var j = i
        while j > 0 and items[j - 1].fused < items[j].fused:
            items.swap_elements(j - 1, j)
            j -= 1
    return items^


def retention(at: String, now_utc: Int) -> Float64:
    """RetentionScore with confidence 1, no reinforcement, and the default half-life.

    The journal has no category or confidence, so every fact decays on the 90-day
    default. An unreadable time counts as no age.
    """
    try:
        var when = parse_instant(at)
        var age = Float64(now_utc - when.utc) / Float64(TICKS_PER_DAY)
        if age < 0:
            age = 0
        return pow(0.5, age / DEFAULT_HALF_LIFE_DAYS)
    except:
        return 1.0


def route_boost(route: String, c: Candidate) -> Float64:
    """The boost RouteScore gives a candidate's type and method for this route.
    """
    var type = String("event")
    if c.kind == "remember" or c.kind == "memory":
        type = "memory"
    elif c.kind == "read":
        type = "chunk"
    var method = String("lexical")
    if c.in_lexical and c.in_vector:
        method = "hybrid"
    elif c.in_vector:
        method = "vector"
    if route == ROUTE_TEMPORAL and (type == "event" or type == "memory"):
        return ROUTE_BOOST
    if route == ROUTE_SEMANTIC and (
        method == "vector" or type == "memory" or type == "chunk"
    ):
        return ROUTE_BOOST
    return 0.0


def route_score(
    c: Candidate,
    route: String,
    now_utc: Int,
    scale: Float64,
    date_boost: Float64,
) -> Float64:
    """Retrieval.RouteScore: the fused score scaled to 0-1, plus the route boost, a
    recency term, and the date boost. Used only to order the candidates."""
    var weight = RETENTION_WEIGHT
    if route == ROUTE_TEMPORAL:
        weight = RETENTION_WEIGHT_TEMPORAL
    return (
        c.fused / scale
        + route_boost(route, c)
        + retention(c.at, now_utc) * weight
        + date_boost
    )


def content_key(text: String) -> String:
    """The lowercased words of a text, joined by one space: the dedupe key."""
    var out = String()
    var word = List[UInt8]()
    var bytes = text.as_bytes()
    for i in range(len(bytes) + 1):
        var c = 32
        if i < len(bytes):
            c = Int(bytes[i])
        if c == 32 or c == 9 or c == 10 or c == 13:
            if len(word) > 0:
                if out.byte_length() > 0:
                    out += " "
                out += String(unsafe_from_utf8=word)
                word = List[UInt8]()
        else:
            if c >= 65 and c <= 90:
                c += 32
            word.append(UInt8(c))
    return out
