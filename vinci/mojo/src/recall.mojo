# Recall over the index: the words of a question become one FTS5 query, and the
# best current versions come back with their journal ids. The tokeniser is
# Retrieval.LexicalQuery and Terms in src/Memory/Retrieval.cs.
#
# Routes are named in a list. Step 2a has "lexical" only. Step 2b adds "vector",
# and fuses the routes' candidates where recall() says so, with abstention.

from std.python import Python, PythonObject

from index_store import IndexStore, py_params
from jsonline import ascii_lower

comptime LEXICAL = "lexical"
comptime DEFAULT_ROUTES = "lexical"

# Retrieval.cs StopWords, in its order. A term in this list is not searched for.
comptime STOP_WORDS = (
    "a an and are about after again all any at be been before between by can"
    " did do does during for from had has have how i if in into is it its me"
    " my no not of on or our out over so some than that the their then there"
    " these this those to under up used we what when where which while who why"
    " with your also"
)


@fieldwise_init
struct Result(Copyable, Movable):
    """One recalled version. id is the journal-style id, for citations."""

    var rowid: Int
    var id: String
    var kind: String
    var text: String
    var at: String
    var source: String
    var trusted: Bool
    # Higher is better. Lexical routes use the negated bm25 value.
    var score: Float64
    var route: String


@fieldwise_init
struct Recall(Movable):
    var query: String
    var results: List[Result]
    # Always false in step 2a. Step 2b decides abstention.
    var abstain: Bool
    var routes: List[String]


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
    store: IndexStore, query: String, limit: Int
) raises -> List[Result]:
    """Candidates from FTS5, best bm25 first. Only current, active versions."""
    var out = List[Result]()
    var match_expr = fts_query(query)
    if match_expr.byte_length() == 0 or limit <= 0:
        return out^
    var rows = store.conn.execute(
        (
            "SELECT f.rowid, f.cite, f.kind, f.text, f.at, f.source, f.trusted,"
            " bm25(fts) AS score FROM fts JOIN facts AS f ON f.rowid ="
            " fts.rowid WHERE fts MATCH ? AND f.valid_until IS NULL AND"
            " f.status = 'active' ORDER BY score LIMIT ?"
        ),
        py_params(PythonObject(match_expr), PythonObject(limit)),
    ).fetchall()
    for row in rows:
        out.append(
            Result(
                Int(String(row[0])),
                String(row[1]),
                String(row[2]),
                String(row[3]),
                String(row[4]),
                String(row[5]),
                String(row[6]) == "1",
                -Float64(String(row[7])),
                LEXICAL,
            )
        )
    return out^


def recall(
    store: IndexStore,
    query: String,
    limit: Int,
    routes: List[String],
) raises -> Recall:
    """Run the routes and return the best limit results.

    With one route its candidates are the answer. Several routes need fusion,
    which step 2b adds, so they are refused here instead of being merged badly.
    """
    if len(routes) == 0:
        raise Error("recall: no route given")
    if len(routes) > 1:
        raise Error("recall: fusing routes is step 2b; give one route")
    var route = routes[0]
    if route != LEXICAL:
        raise Error("recall: route " + route + " is not built yet (step 2b)")
    var found = lexical(store, query, limit)
    return Recall(query, found^, False, routes.copy())
