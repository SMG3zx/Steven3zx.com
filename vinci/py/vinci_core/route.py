"""Query routing: which routes a question uses, and how complex it is.

A word is a run of [a-z0-9_] bytes in the lowercased query, and a phrase is a run of words.
Non-ASCII letters separate words, a known gap.

Port of mojo/src/route.mojo.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

TEMPORAL_WORDS = "when|current|currently|before|after|last|previous|recent|latest|used to"
PROCEDURAL_WORDS = "how do i|how did we|steps|procedure|runbook|recover|deploy|configure|fix"
GRAPH_WORDS = "who|which|what depends|related|connected|relationship|linked|associated|affect"
MULTI_HOP_WORDS = "and|both|between|depend|affect|why|compare|related"

ROUTE_SEMANTIC = "semantic"
ROUTE_TEMPORAL = "temporal"
ROUTE_PROCEDURAL = "procedural"
ROUTE_GRAPH = "graph"

_WORD = re.compile(r"[a-z0-9_]+")


def word_tokens(text: str) -> list[str]:
    """The lowercased words of text: maximal runs of [a-z0-9_]."""
    return _WORD.findall(text.lower())


def has_phrase(tokens: list[str], phrase: str) -> bool:
    """True when the phrase's words appear in order and next to each other."""
    words = phrase.split(" ")
    if len(words) == 0 or len(tokens) < len(words):
        return False
    for start in range(len(tokens) - len(words) + 1):
        if all(tokens[start + k] == words[k] for k in range(len(words))):
            return True
    return False


def any_phrase(tokens: list[str], alternatives: str) -> bool:
    """True when any of the |-separated phrases occurs in the tokens."""
    return any(has_phrase(tokens, alt) for alt in alternatives.split("|"))


def route_query(query: str) -> str:
    """Retrieval.RouteQuery: temporal, procedural, graph or semantic, in that order."""
    tokens = word_tokens(query)
    if any_phrase(tokens, TEMPORAL_WORDS):
        return ROUTE_TEMPORAL
    if any_phrase(tokens, PROCEDURAL_WORDS):
        return ROUTE_PROCEDURAL
    if any_phrase(tokens, GRAPH_WORDS):
        return ROUTE_GRAPH
    return ROUTE_SEMANTIC


@dataclass(frozen=True)
class Plan:
    """How many candidates to fetch for a question of this complexity. There is no graph
    route and no reranker."""

    complexity: str
    candidate_multiplier: int


def plan_of(query: str, route: str) -> Plan:
    """ClassifyQueryComplexity and the candidate multiplier for it."""
    complexity = "simple"
    if route == ROUTE_TEMPORAL:
        complexity = "temporal"
    elif route == ROUTE_PROCEDURAL:
        complexity = "procedural"
    elif route == ROUTE_GRAPH or any_phrase(word_tokens(query), MULTI_HOP_WORDS):
        complexity = "multi-hop"
    multiplier = 2
    if complexity == "multi-hop":
        multiplier = 6
    elif complexity != "simple":
        multiplier = 4
    return Plan(complexity, multiplier)
