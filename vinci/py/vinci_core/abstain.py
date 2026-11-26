"""Abstention and the corrective query: when the retrieved evidence is too weak to answer
from, recall says so, and a second query is built from the question's own terms.

A candidate's vector cosine is the real cosine, in -1 to 1, so VECTOR_EVIDENCE_COSINE is a
threshold on cosine.

Port of mojo/src/abstain.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass

from .fusion import Candidate
from .jsonline import ascii_lower
from .terms import query_terms

VECTOR_EVIDENCE_COSINE = 0.41
WEAK_SCORE = 0.18
NO_EVIDENCE_SCORE = 0.25
WEAK_REASON = "Retrieved evidence did not meet the confidence threshold."
NO_EVIDENCE_REASON = "No relevant evidence was retrieved."
NO_IDENTIFIER_REASON = "No retrieved evidence contains the query's distinctive identifiers."
NO_TERMS_REASON = "Retrieved evidence did not contain query-linked terms."
CORRECTIVE_TERMS = 10


@dataclass
class Abstention:
    abstain: bool
    reason: str


@dataclass
class Assessment:
    """EvidenceAssessment: the count, and whether the best score needs a second try."""

    count: int
    max_score: float
    needs_correction: bool


def assess_evidence(results: list[Candidate]) -> Assessment:
    best = 0.0
    for c in results:
        if c.fused > best:
            best = c.fused
    count = len(results)
    return Assessment(count, best, count == 0 or best < NO_EVIDENCE_SCORE)


def mentions(content: str, term: str) -> bool:
    """A term matches its own text, or its first five letters when it has six or more, so
    "correction" finds "Correct"."""
    if term in content:
        return True
    if len(term) >= 6:
        return term[:5] in content
    return False


def is_distinctive(term: str) -> bool:
    """A term with a digit, a hyphen or an underscore, or fourteen or more letters."""
    if len(term) >= 14:
        return True
    return any(("0" <= ch <= "9") or ch in "-_" for ch in term)


def any_mention(results: list[Candidate], term: str) -> bool:
    return any(mentions(ascii_lower(r.text), term) for r in results)


def should_abstain(results: list[Candidate], query: str) -> Abstention:
    """Retrieval.ShouldAbstain. The rules, in order:

    1. No results: abstain.
    2. Every distinctive term (digits, hyphens, underscores, 14+ letters) must appear in some
       result. Otherwise abstain.
    3. The results must cover at least half of the query's terms (one shared word is not
       enough), or some result must have a cosine of at least 0.41. Otherwise abstain.
    4. A best fused score below 0.18 with no term coverage: abstain.
    """
    if len(results) == 0:
        return Abstention(True, NO_EVIDENCE_REASON)
    best = 0.0
    for r in results:
        if r.fused > best:
            best = r.fused
    meaningful = query_terms(query)
    covered = sum(1 for term in meaningful if any_mention(results, term))
    close = any(r.has_cosine and r.cosine >= VECTOR_EVIDENCE_COSINE for r in results)
    has_lexical = (covered * 2 >= len(meaningful) and covered > 0) or close
    for term in meaningful:
        if is_distinctive(term) and not any_mention(results, term):
            return Abstention(True, NO_IDENTIFIER_REASON)
    if len(meaningful) > 0 and not has_lexical:
        return Abstention(True, NO_TERMS_REASON)
    if best < WEAK_SCORE and not has_lexical:
        return Abstention(True, WEAK_REASON)
    return Abstention(False, "")


def corrective_query(query: str) -> str:
    """The question's distinct terms, at most ten, in first-seen order. Empty when it would be
    the same question (Retrieval.CorrectiveQuery returns null then)."""
    seen: list[str] = []
    for term in query_terms(query):
        if len(seen) >= CORRECTIVE_TERMS:
            break
        if term not in seen:
            seen.append(term)
    out = " ".join(seen)
    same = query.strip().lower()
    if out == "" or out == same:
        return ""
    return out
