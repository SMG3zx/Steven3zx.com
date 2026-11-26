"""The citation check for the chat: every cited id must come from this turn's tool results.

A citation is a bracketed token of the answer that is either an id as journal recall prints it, a
letter, digits, and for a read passage a slash and more digits ([i1], [m12], [L142], [d1/2]), or
any token that appears bracketed in this turn's tool results (an id of another shape, such as an
evaluation's [answer_d8e33f5c_2:0]). Each cited id must appear in the tool results of the same
turn, as "[id]". A journal-shaped id that the results do not show is reported. Anything else in
brackets is ordinary text.

Port of mojo/src/chat_cite.mojo.
"""

from __future__ import annotations

OPEN_BRACKET = 91
CLOSE_BRACKET = 93
MAX_CITE_BYTES = 80
SLASH = 47


def is_digit(c: int) -> bool:
    return 48 <= c <= 57


def is_letter(c: int) -> bool:
    return (65 <= c <= 90) or (97 <= c <= 122)


def is_cite_id(text: str) -> bool:
    """A letter, one or more digits, and an optional /digits: i1, L142, d1/2."""
    b = text.encode("utf-8")
    n = len(b)
    if n < 2 or not is_letter(b[0]):
        return False
    i = 1
    digits = 0
    while i < n and is_digit(b[i]):
        digits += 1
        i += 1
    if digits == 0:
        return False
    if i == n:
        return True
    if b[i] != SLASH:
        return False
    i += 1
    more = 0
    while i < n and is_digit(b[i]):
        more += 1
        i += 1
    return more > 0 and i == n


def bracketed_tokens(answer: str) -> list[str]:
    """The distinct bracketed tokens of an answer, in the order they first appear. A bracket pair
    with white space, a second bracket, or a long run between is not a token."""
    out: list[str] = []
    b = answer.encode("utf-8")
    n = len(b)
    i = 0
    while i < n:
        if b[i] != OPEN_BRACKET:
            i += 1
            continue
        j = i + 1
        clean = True
        while j < n and b[j] != CLOSE_BRACKET:
            c = b[j]
            if c in (32, 9, 10, 13, OPEN_BRACKET):
                clean = False
                break
            if j - i > MAX_CITE_BYTES:
                clean = False
                break
            j += 1
        if clean and j < n and b[j] == CLOSE_BRACKET and j > i + 1:
            token = b[i + 1 : j].decode("utf-8")
            if token not in out:
                out.append(token)
            i = j + 1
        else:
            i += 1
    return out


def cited_ids(answer: str) -> list[str]:
    """The distinct bracketed tokens of an answer that have the shape of a journal id."""
    return [t for t in bracketed_tokens(answer) if is_cite_id(t)]


def cited_ids_with_evidence(answer: str, evidence: str) -> list[str]:
    """The citations of an answer: the journal-shaped ids, and any other bracketed token that the
    tool results of this turn show as "[token]". Ordinary bracketed text is neither."""
    return [t for t in bracketed_tokens(answer) if is_cite_id(t) or ("[" + t + "]") in evidence]


def unseen_cites(cited: list[str], evidence: str) -> list[str]:
    """The cited ids whose "[id]" does not appear in the evidence (the tool results of this turn)."""
    return [c for c in cited if ("[" + c + "]") not in evidence]


def document_base(ident: str) -> str:
    """The document a passage belongs to: d1/2 is d1. Other ids are their own base."""
    cut = ident.find("/")
    return ident if cut < 0 else ident[:cut]


def is_document_cite(ident: str) -> bool:
    """A read document or one of its passages: d followed by digits, with an optional /digits."""
    return ident.startswith("d") and is_cite_id(ident)
