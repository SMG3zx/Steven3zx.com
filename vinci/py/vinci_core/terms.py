"""Query terms: the words a question searches for, and the FTS5 phrase quoting.

Shared by recall and abstain, which would otherwise import each other.

Ported from the term helpers in mojo/src/recall.mojo.
"""

from __future__ import annotations

STOP_WORDS = frozenset(
    (
        "a an and are about after again all any at be been before between by can "
        "did do does during for from had has have how i if in into is it its me "
        "my no not of on or our out over so some than that the their then there "
        "these this those to under up used we what when where which while who why "
        "with your also"
    ).split(" ")
)


def is_term_byte(c: int) -> bool:
    """Bytes allowed after the first byte of a term: a-z, 0-9, underscore, hyphen."""
    return (97 <= c <= 122) or (48 <= c <= 57) or c == 95 or c == 45


def is_term_start(c: int) -> bool:
    """Bytes that can start a term: a-z and 0-9."""
    return (97 <= c <= 122) or (48 <= c <= 57)


def is_stop_word(term: str) -> bool:
    return term in STOP_WORDS


def query_terms(query: str) -> list[str]:
    """Retrieval.Terms: lowercase, then runs [a-z0-9][a-z0-9_-]{2,}, minus stop words.

    Runs shorter than three bytes are dropped, as the regular expression drops them.
    Non-ASCII letters are not terms.
    """
    out: list[str] = []
    src = "".join(chr(ord(ch) + 32) if "A" <= ch <= "Z" else ch for ch in query).encode("utf-8")
    n = len(src)
    i = 0
    while i < n:
        if not is_term_start(src[i]):
            i += 1
            continue
        j = i + 1
        while j < n and is_term_byte(src[j]):
            j += 1
        if j - i < 3:
            i += 1
            continue
        word = src[i:j].decode("ascii")
        if not is_stop_word(word):
            out.append(word)
        i = j
    return out


def quote_phrase(word: str) -> str:
    """One FTS5 phrase. A double quote inside the word is doubled, so no input is syntax."""
    return '"' + word.replace('"', '""') + '"'


def fts_query(query: str) -> str:
    """The FTS5 MATCH expression for a question: its terms, quoted and joined with OR.

    When no term is left (only stop words, or no letters), the question's own words are quoted
    instead. Empty when there is nothing to search for.
    """
    terms = query_terms(query)
    if len(terms) > 0:
        parts = [quote_phrase(t) for t in terms]
    else:
        parts = [quote_phrase(w) for w in query.split(" ") if len(w) > 0]
    return " OR ".join(parts)
