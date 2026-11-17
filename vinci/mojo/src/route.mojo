# Query routing, ported from Retrieval.RouteQuery, ClassifyQueryComplexity and Plan
# in src/Memory/Retrieval.cs. The C# regular expressions match whole words; here a
# word is a run of [a-z0-9_] bytes in the lowercased query, and a phrase is a run of
# words. Non-ASCII letters separate words (the C# \b would keep them), a known gap.

comptime TEMPORAL_WORDS = (
    "when|current|currently|before|after|last|previous|recent|latest|used to"
)
comptime PROCEDURAL_WORDS = (
    "how do i|how did we|steps|procedure|runbook|recover|deploy|configure|fix"
)
comptime GRAPH_WORDS = (
    "who|which|what"
    " depends|related|connected|relationship|linked|associated|affect"
)
comptime MULTI_HOP_WORDS = "and|both|between|depend|affect|why|compare|related"

comptime ROUTE_SEMANTIC = "semantic"
comptime ROUTE_TEMPORAL = "temporal"
comptime ROUTE_PROCEDURAL = "procedural"
comptime ROUTE_GRAPH = "graph"


def is_word_byte(c: Int) -> Bool:
    return (c >= 97 and c <= 122) or (c >= 48 and c <= 57) or c == 95


def word_tokens(text: String) -> List[String]:
    """The lowercased words of text: maximal runs of [a-z0-9_]."""
    var out = List[String]()
    var bytes = text.lower().as_bytes()
    var start = -1
    for i in range(len(bytes) + 1):
        var c = -1
        if i < len(bytes):
            c = Int(bytes[i])
            if c >= 65 and c <= 90:
                c += 32
        if c >= 0 and is_word_byte(c):
            if start < 0:
                start = i
        elif start >= 0:
            var word = List[UInt8]()
            for k in range(start, i):
                var b = bytes[k]
                if Int(b) >= 65 and Int(b) <= 90:
                    b = UInt8(Int(b) + 32)
                word.append(b)
            out.append(String(unsafe_from_utf8=word))
            start = -1
    return out^


def has_phrase(tokens: List[String], phrase: String) raises -> Bool:
    """True when the phrase's words appear in order and next to each other."""
    var words = List[String]()
    for w in phrase.split(" "):
        words.append(String(w))
    if len(words) == 0 or len(tokens) < len(words):
        return False
    for start in range(len(tokens) - len(words) + 1):
        var matched = True
        for k in range(len(words)):
            if tokens[start + k] != words[k]:
                matched = False
                break
        if matched:
            return True
    return False


def any_phrase(tokens: List[String], alternatives: String) raises -> Bool:
    """True when any of the |-separated phrases occurs in the tokens."""
    for alt in alternatives.split("|"):
        if has_phrase(tokens, String(alt)):
            return True
    return False


def route_query(query: String) raises -> String:
    """Retrieval.RouteQuery: temporal, procedural, graph or semantic, in that order.
    """
    var tokens = word_tokens(query)
    if any_phrase(tokens, TEMPORAL_WORDS):
        return ROUTE_TEMPORAL
    if any_phrase(tokens, PROCEDURAL_WORDS):
        return ROUTE_PROCEDURAL
    if any_phrase(tokens, GRAPH_WORDS):
        return ROUTE_GRAPH
    return ROUTE_SEMANTIC


struct Plan(Copyable, Movable):
    """Retrieval.Plan, without the graph and rerank limits (no graph route, no
    reranker in this port)."""

    var complexity: String
    var candidate_multiplier: Int

    def __init__(out self, var complexity: String, candidate_multiplier: Int):
        self.complexity = complexity^
        self.candidate_multiplier = candidate_multiplier


def plan_of(query: String, route: String) raises -> Plan:
    """ClassifyQueryComplexity and the candidate multiplier for it."""
    var complexity = String("simple")
    if route == ROUTE_TEMPORAL:
        complexity = "temporal"
    elif route == ROUTE_PROCEDURAL:
        complexity = "procedural"
    elif route == ROUTE_GRAPH or any_phrase(
        word_tokens(query), MULTI_HOP_WORDS
    ):
        complexity = "multi-hop"
    var multiplier = 2
    if complexity == "multi-hop":
        multiplier = 6
    elif complexity != "simple":
        multiplier = 4
    return Plan(complexity^, multiplier)
