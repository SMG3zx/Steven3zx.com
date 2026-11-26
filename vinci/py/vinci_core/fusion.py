"""Reciprocal-rank fusion, the final ordering, and the content dedupe.

Raw scores (bm25, cosine) are not comparable across routes, so each route votes by rank: a
candidate at rank i (0-based) in route r adds weight_r / (k + i + 1), with k = RRF_K = 60
unless an eval sets another. The sum is the fused score.

Port of mojo/src/fusion.mojo.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, replace
from typing import Optional

from .route import ROUTE_GRAPH, ROUTE_PROCEDURAL, ROUTE_SEMANTIC, ROUTE_TEMPORAL
from .session import UserError, user_error
from .timeutil import TICKS_PER_DAY, parse_instant

RRF_K = 60
DEFAULT_SEMANTIC = "0.25,1.0,0.2"
DEFAULT_HALF_LIFE_DAYS = 90.0
ROUTE_BOOST = 0.1
RETENTION_WEIGHT = 0.025
RETENTION_WEIGHT_TEMPORAL = 0.08
DATE_BOOST = 0.1


@dataclass
class Candidate:
    """One fact as recall returns it. The routes fill the flags, fusion the score.

    rowid is the fact version (the key the routes agree on). id is the citation (the journal
    id), kind the journal kind. at and valid_from are journal times.
    """

    rowid: int
    id: str
    kind: str
    text: str
    at: str
    valid_from: str
    source: str
    trusted: bool
    in_lexical: bool = False
    in_vector: bool = False
    fused: float = 0.0
    has_cosine: bool = False
    cosine: float = 0.0


@dataclass(frozen=True)
class Weights:
    """Retrieval's RankFusion.Weights. graph is kept for the table; no graph route runs.

    rrf_k is the reciprocal-rank constant (RRF_K unless an eval sets another). semantic_only
    limits an eval override to route semantic (scope=semantic).
    """

    lexical: float
    vector: float
    graph: float
    rrf_k: int = RRF_K
    semantic_only: bool = False

    def of(self, method: str) -> float:
        if method == "lexical":
            return self.lexical
        if method == "vector":
            return self.vector
        return self.graph


def semantic_weights() -> Weights:
    """The weights for route semantic: FUSION_SEMANTIC "lexical,vector,graph" when it is
    three numbers, else 0.25, 1.0, 0.2."""
    raw = os.environ.get("FUSION_SEMANTIC", DEFAULT_SEMANTIC)
    parts = raw.split(",")
    if len(parts) == 3:
        try:
            return Weights(float(parts[0]), float(parts[1]), float(parts[2]))
        except ValueError:
            pass
    return Weights(0.25, 1.0, 0.2)


def parse_weights(text: str) -> Weights:
    """The eval-only fusion override: "lexical=W,vector=W" and optionally ",k=K", from the
    recall flag --weights or the environment variable VINCI_RECALL_WEIGHTS.

    Both weights are needed, neither may be negative, and their sum must be above zero. Only
    their ratio matters to the ordering. k is the RRF constant and must be at least 1.
    Anything else is an error, so a typo is not a silent default.
    """
    lexical = -1.0
    vector = -1.0
    rrf_k = RRF_K
    semantic_only = False
    for part in text.split(","):
        pair = part.split("=")
        if len(pair) != 2:
            raise user_error("weights: each item is name=value, got '" + part + "'")
        name, value_text = pair[0], pair[1]
        if name == "k":
            try:
                rrf_k = int(value_text)
            except ValueError:
                raise user_error("weights: k must be a whole number") from None
            if rrf_k < 1:
                raise user_error("weights: k must be at least 1")
        elif name == "scope":
            if value_text == "semantic":
                semantic_only = True
            elif value_text != "all":
                raise user_error("weights: scope is all or semantic")
        elif name in ("lexical", "vector"):
            try:
                value = float(value_text)
            except ValueError:
                raise user_error("weights: " + name + " must be a number") from None
            if value < 0:
                raise user_error("weights: " + name + " must not be negative")
            if name == "lexical":
                lexical = value
            else:
                vector = value
        else:
            raise user_error("weights: unknown name '" + name + "' (lexical, vector, k, scope)")
    if lexical < 0 or vector < 0:
        raise user_error("weights: give both lexical=W and vector=W")
    if lexical + vector <= 0:
        raise user_error("weights: lexical and vector cannot both be zero")
    return Weights(lexical, vector, 0.0, rrf_k, semantic_only)


def weights_for(route: str) -> Weights:
    """RankFusion.Weights.For: graph, temporal and procedural have their own mixes."""
    if route == ROUTE_GRAPH:
        return Weights(0.25, 0.2, 0.55)
    if route == ROUTE_TEMPORAL:
        return Weights(0.45, 0.2, 0.35)
    if route == ROUTE_PROCEDURAL:
        return Weights(0.5, 0.2, 0.3)
    return semantic_weights()


def fuse(lexical: list[Candidate], vector: list[Candidate], weights: Weights) -> list[Candidate]:
    """RankFusion.Fuse over two ranked lists (each already best first).

    A fact found by both routes is one candidate, with both flags and the summed score. The
    output is ordered by fused score, best first; ties keep the order the candidates were
    first seen in (lexical, then vector), as a stable sort does.
    """
    out: list[Candidate] = []
    position: dict[int, int] = {}
    for leg in range(2):
        method = "lexical" if leg == 0 else "vector"
        ranked = lexical if leg == 0 else vector
        contribution_weight = weights.of(method)
        for i, cand in enumerate(ranked):
            contribution = contribution_weight / float(weights.rrf_k + i + 1)
            rowid = cand.rowid
            if rowid not in position:
                first = replace(cand)
                first.in_lexical = leg == 0
                first.in_vector = leg == 1
                first.fused = contribution
                position[rowid] = len(out)
                out.append(first)
            else:
                at = position[rowid]
                if leg == 0:
                    out[at].in_lexical = True
                else:
                    out[at].in_vector = True
                    out[at].has_cosine = cand.has_cosine
                    out[at].cosine = cand.cosine
                out[at].fused += contribution
    return stable_sort_by_fused(out)


def stable_sort_by_fused(items: list[Candidate]) -> list[Candidate]:
    """Stable sort by fused score, descending (Python's sort is stable)."""
    return sorted(items, key=lambda c: c.fused, reverse=True)


def retention(at: str, now_utc: int) -> float:
    """RetentionScore with confidence 1, no reinforcement, and the default half-life. An
    unreadable time counts as no age."""
    try:
        when = parse_instant(at)
        age = float(now_utc - when.utc) / float(TICKS_PER_DAY)
        if age < 0:
            age = 0.0
        return 0.5 ** (age / DEFAULT_HALF_LIFE_DAYS)
    except ValueError:
        return 1.0


def route_boost(route: str, c: Candidate) -> float:
    """The boost RouteScore gives a candidate's type and method for this route."""
    kind_type = "event"
    if c.kind in ("remember", "memory"):
        kind_type = "memory"
    elif c.kind == "read":
        kind_type = "chunk"
    method = "lexical"
    if c.in_lexical and c.in_vector:
        method = "hybrid"
    elif c.in_vector:
        method = "vector"
    if route == ROUTE_TEMPORAL and kind_type in ("event", "memory"):
        return ROUTE_BOOST
    if route == ROUTE_SEMANTIC and (method == "vector" or kind_type in ("memory", "chunk")):
        return ROUTE_BOOST
    return 0.0


def route_score(c: Candidate, route: str, now_utc: int, scale: float, date_boost: float) -> float:
    """The fused score scaled to 0-1, plus the route boost, a recency term, and the date
    boost. Used only to order the candidates."""
    weight = RETENTION_WEIGHT_TEMPORAL if route == ROUTE_TEMPORAL else RETENTION_WEIGHT
    return c.fused / scale + route_boost(route, c) + retention(c.at, now_utc) * weight + date_boost


def content_key(text: str) -> str:
    """The lowercased words of a text, joined by one space: the dedupe key."""
    words: list[str] = []
    current: list[str] = []
    for ch in text + " ":
        if ch in " \t\n\r":
            if current:
                words.append("".join(current))
                current = []
        else:
            current.append(ch.lower() if "A" <= ch <= "Z" else ch)
    return " ".join(words)
