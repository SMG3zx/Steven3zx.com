# Abstention and the corrective query: when the retrieved evidence is too weak to
# answer from, recall says so, and a second query is built from the question's own terms.
# A candidate's vector cosine is the real cosine, in -1 to 1, so VECTOR_EVIDENCE_COSINE
# is a threshold on cosine.

from recall import query_terms
from fusion import Candidate
from jsonline import ascii_lower
from timeutil import ascii_prefix

comptime VECTOR_EVIDENCE_COSINE = 0.41
comptime WEAK_SCORE = 0.18
comptime NO_EVIDENCE_SCORE = 0.25
comptime WEAK_REASON = (
    "Retrieved evidence did not meet the confidence threshold."
)
comptime NO_EVIDENCE_REASON = "No relevant evidence was retrieved."
comptime NO_IDENTIFIER_REASON = (
    "No retrieved evidence contains the query's distinctive identifiers."
)
comptime NO_TERMS_REASON = (
    "Retrieved evidence did not contain query-linked terms."
)
comptime CORRECTIVE_TERMS = 10


struct Abstention(Copyable, Movable):
    var abstain: Bool
    var reason: String

    def __init__(out self, abstain: Bool, var reason: String):
        self.abstain = abstain
        self.reason = reason^


@fieldwise_init
struct Assessment(Copyable, Movable):
    """EvidenceAssessment: the count, and whether the best score needs a second try.
    """

    var count: Int
    var max_score: Float64
    var needs_correction: Bool


def assess_evidence(results: List[Candidate]) -> Assessment:
    var best: Float64 = 0
    for c in results:
        if c.fused > best:
            best = c.fused
    var count = len(results)
    return Assessment(count, best, count == 0 or best < NO_EVIDENCE_SCORE)


def mentions(content: String, term: String) -> Bool:
    """A term matches its own text, or its first five letters when it has six or more,
    so "correction" finds "Correct"."""
    if content.find(term) >= 0:
        return True
    if term.byte_length() >= 6:
        return content.find(ascii_prefix(term, 5)) >= 0
    return False


def is_distinctive(term: String) -> Bool:
    """A term with a digit, a hyphen or an underscore, or fourteen or more letters.
    """
    if term.byte_length() >= 14:
        return True
    for b in term.as_bytes():
        var c = Int(b)
        if (c >= 48 and c <= 57) or c == 45 or c == 95:
            return True
    return False


def any_mention(results: List[Candidate], term: String) -> Bool:
    for r in results:
        if mentions(ascii_lower(r.text), term):
            return True
    return False


def should_abstain(
    results: List[Candidate], query: String
) raises -> Abstention:
    """Retrieval.ShouldAbstain. The rules, in order:

    1. No results: abstain.
    2. Every distinctive term (digits, hyphens, underscores, 14+ letters) must appear
       in some result. Otherwise abstain.
    3. The results must cover at least half of the query's terms (one shared word is
       not enough), or some result must have a cosine of at least 0.41. Otherwise
       abstain.
    4. A best fused score below 0.18 with no term coverage: abstain.
    """
    if len(results) == 0:
        return Abstention(True, NO_EVIDENCE_REASON)
    var best: Float64 = 0
    for r in results:
        if r.fused > best:
            best = r.fused
    var meaningful = query_terms(query)
    var covered = 0
    for term in meaningful:
        if any_mention(results, term):
            covered += 1
    var close = False
    for r in results:
        if r.has_cosine and r.cosine >= VECTOR_EVIDENCE_COSINE:
            close = True
    var has_lexical = (covered * 2 >= len(meaningful) and covered > 0) or close
    for term in meaningful:
        if is_distinctive(term) and not any_mention(results, term):
            return Abstention(True, NO_IDENTIFIER_REASON)
    if len(meaningful) > 0 and not has_lexical:
        return Abstention(True, NO_TERMS_REASON)
    if best < WEAK_SCORE and not has_lexical:
        return Abstention(True, WEAK_REASON)
    return Abstention(False, "")


def corrective_query(query: String) raises -> String:
    """The question's distinct terms, at most ten, in first-seen order. Empty when it
    would be the same question (Retrieval.CorrectiveQuery returns null then)."""
    var seen = List[String]()
    for term in query_terms(query):
        if len(seen) >= CORRECTIVE_TERMS:
            break
        var known = False
        for s in seen:
            if s == term:
                known = True
                break
        if not known:
            seen.append(term)
    var out = String()
    for i in range(len(seen)):
        if i > 0:
            out += " "
        out += seen[i]
    var same = ascii_lower(String(query.strip()))
    if out.byte_length() == 0 or out == same:
        return ""
    return out
