"""Budgeted recall: the packed context.

Recall's results come best first. The packer puts the best `whole` of them in whole, then one
line each for the rest, and stops before the budget is exceeded. A line is:

  whole:    [<id>] <date> <kind>: <text>          (the text as stored, newlines kept)
  pointer:  [<id>] <date> <kind>: <digest> (open <id> for more)
            (the open hint only when the digest leaves text out)

A digest is the first 100 characters with whitespace collapsed, and an ellipsis when cut
(handles.digest_of). A candidate for the whole form that does not fit the budget with the lines
before it is shown as a pointer instead. One that does not fit even alone is counted as
oversized, and it is shown as a pointer too. The packer stops at the first pointer that does
not fit, so the output keeps rank order and omits the rest.

Tokens: the estimate is ceil(bytes / 4) of the packed text. With a tokenizer URL the count is
exact: llama-server's /tokenize is asked for the whole text each time.

Port of mojo/src/pack.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass

from vinci_edge import llama_count

from .fusion import Candidate
from .handles import date_of, digest_is_cut, digest_of
from .session import user_error

DEFAULT_BUDGET = 2000
DEFAULT_WHOLE = 2
METHOD_ESTIMATE = "estimate"
METHOD_SERVER = "llama-server"
METHOD_WORDS = "words"


@dataclass
class Counter:
    """How a packed text is measured. method is estimate, llama-server (url is the full
    /tokenize URL) or words (one token per whitespace-separated word: a fake for tests)."""

    method: str
    url: str = ""

    def count(self, text: str) -> int:
        if self.method == METHOD_SERVER:
            return int(llama_count.count_tokens(self.url, text))
        if self.method == METHOD_WORDS:
            return word_count(text)
        return (len(text.encode("utf-8")) + 3) // 4


def estimate_counter() -> Counter:
    return Counter(METHOD_ESTIMATE, "")


def word_count(text: str) -> int:
    """Non-empty runs of characters other than space, tab, newline and carriage return."""
    return len([w for w in text.replace("\t", " ").replace("\n", " ").replace("\r", " ").split(" ") if w])


@dataclass
class Packed:
    """The packed text and what went into it. tokens is the count of text, by method."""

    text: str
    tokens: int
    budget: int
    method: str
    whole: int
    pointers: int
    oversized: int
    omitted: int


def joined(head: str, line: str) -> str:
    if len(head) == 0:
        return line
    return head + "\n" + line


def label_of(c: Candidate) -> str:
    """The kind as the packed line shows it. A feed record says where it came from, so the
    model does not take a commit for a journal line: "git commit", "vault section"."""
    if c.source == "git":
        return "git " + c.kind
    if c.source == "vaults":
        return "vault " + c.kind
    if c.source == "activity":
        return "window time"
    return c.kind


def whole_line(c: Candidate) -> str:
    return "[" + c.id + "] " + date_of(c.at) + " " + label_of(c) + ": " + c.text


def pointer_line(c: Candidate) -> str:
    """The digest of a record. The open hint is only added when the digest leaves text out:
    for a short record the digest is the whole text, and the hint would say there is more to
    open when there is not."""
    line = "[" + c.id + "] " + date_of(c.at) + " " + label_of(c) + ": " + digest_of(c.text)
    if digest_is_cut(c.text):
        line += " (open " + c.id + " for more)"
    return line


def pack(results: list[Candidate], budget: int, whole: int, counter: Counter) -> Packed:
    """The packed context for results (best first) within budget tokens."""
    if budget < 1:
        raise user_error("--budget must be at least 1")
    if whole < 0:
        raise user_error("--whole must be 0 or more")
    out = ""
    tokens = 0
    whole_n = 0
    pointer_n = 0
    oversized = 0
    for i, c in enumerate(results):
        if i < whole:
            full = joined(out, whole_line(c))
            full_tokens = counter.count(full)
            if full_tokens <= budget:
                out = full
                tokens = full_tokens
                whole_n += 1
                continue
            if counter.count(whole_line(c)) > budget:
                oversized += 1
        pointer = joined(out, pointer_line(c))
        pointer_tokens = counter.count(pointer)
        if pointer_tokens > budget:
            break
        out = pointer
        tokens = pointer_tokens
        pointer_n += 1
    return Packed(out, tokens, budget, counter.method, whole_n, pointer_n, oversized, len(results) - whole_n - pointer_n)
