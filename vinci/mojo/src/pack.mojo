# Budgeted recall (M3 step 3a, phase 3 of the one-journal plan): the packed context.
#
# Recall's results come best first. The packer puts the best `whole` of them in whole,
# then one line each for the rest, and stops before the budget is exceeded. A line is:
#
#   whole:    [<id>] <date> <kind>: <text>          (the text as stored, newlines kept)
#   pointer:  [<id>] <date> <kind>: <digest> (open <id> for more)
#             (the open hint only when the digest leaves text out)
#
# A digest is the first 100 characters with whitespace collapsed, and "…" when cut
# (handles.digest_of). A candidate for the whole form that does not fit the budget with
# the lines before it is shown as a pointer instead. One that does not fit even alone is
# counted as oversized, and it is shown as a pointer too. The packer stops at the first
# pointer that does not fit, so the output keeps rank order and omits the rest.
#
# Tokens: the estimate is ceil(bytes / 4) of the packed text. With a tokenizer URL the
# count is exact: llama-server's /tokenize is asked for the whole text each time, because
# its array input does not give per-item counts. The count is always for the packed text,
# so it is never over the budget.

from std.python import Python, PythonObject

from embed_edge import edge_module
from fusion import Candidate
from handles import date_of, digest_is_cut, digest_of
from session import user_error

comptime DEFAULT_BUDGET = 2000
comptime DEFAULT_WHOLE = 2
comptime METHOD_ESTIMATE = "estimate"
comptime METHOD_SERVER = "llama-server"
comptime METHOD_WORDS = "words"


struct Counter(Copyable, Movable):
    """How a packed text is measured. method is estimate, llama-server (url is the full
    /tokenize URL) or words (one token per whitespace-separated word: a fake for tests).
    """

    var method: String
    var url: String

    def __init__(out self, var method: String, var url: String):
        self.method = method^
        self.url = url^

    def count(self, text: String) raises -> Int:
        if self.method == METHOD_SERVER:
            _ = edge_module()
            var tokens = Python.import_module("vinci_edge.llama_count")
            return Int(
                String(
                    tokens.count_tokens(
                        PythonObject(self.url), PythonObject(text)
                    )
                )
            )
        if self.method == METHOD_WORDS:
            return word_count(text)
        return (text.byte_length() + 3) // 4


def estimate_counter() -> Counter:
    return Counter(String(METHOD_ESTIMATE), String(""))


def word_count(text: String) -> Int:
    """Non-empty runs of bytes other than space, tab, newline and carriage return.
    """
    var n = 0
    var inside = False
    for b in text.as_bytes():
        var c = Int(b)
        var space = c == 32 or c == 9 or c == 10 or c == 13
        if space:
            inside = False
        elif not inside:
            inside = True
            n += 1
    return n


@fieldwise_init
struct Packed(Copyable, Movable):
    """The packed text and what went into it. tokens is the count of text, by method.
    """

    var text: String
    var tokens: Int
    var budget: Int
    var method: String
    var whole: Int
    var pointers: Int
    var oversized: Int
    var omitted: Int


def joined(head: String, line: String) -> String:
    if head.byte_length() == 0:
        return line.copy()
    return head + "\n" + line


def label_of(c: Candidate) -> String:
    """The kind as the packed line shows it. A feed record says where it came from, so
    the model does not take a commit for a journal line: "git commit", "vault section".
    """
    if c.source == "git":
        return "git " + c.kind
    if c.source == "vaults":
        return "vault " + c.kind
    return c.kind


def whole_line(c: Candidate) -> String:
    return "[" + c.id + "] " + date_of(c.at) + " " + label_of(c) + ": " + c.text


def pointer_line(c: Candidate) raises -> String:
    """The digest of a record. The open hint is only added when the digest leaves text
    out: for a short record the digest is the whole text, and the hint would say there
    is more to open when there is not."""
    var line = (
        "["
        + c.id
        + "] "
        + date_of(c.at)
        + " "
        + label_of(c)
        + ": "
        + digest_of(c.text)
    )
    if digest_is_cut(c.text):
        line += " (open " + c.id + " for more)"
    return line


def pack(
    results: List[Candidate],
    budget: Int,
    whole: Int,
    counter: Counter,
) raises -> Packed:
    """The packed context for results (best first) within budget tokens."""
    if budget < 1:
        raise user_error("--budget must be at least 1")
    if whole < 0:
        raise user_error("--whole must be 0 or more")
    var out = String()
    var tokens = 0
    var whole_n = 0
    var pointer_n = 0
    var oversized = 0
    for i in range(len(results)):
        var c = results[i].copy()
        if i < whole:
            var full = joined(out, whole_line(c))
            var full_tokens = counter.count(full)
            if full_tokens <= budget:
                out = full^
                tokens = full_tokens
                whole_n += 1
                continue
            if counter.count(whole_line(c)) > budget:
                oversized += 1
        var pointer = joined(out, pointer_line(c))
        var pointer_tokens = counter.count(pointer)
        if pointer_tokens > budget:
            break
        out = pointer^
        tokens = pointer_tokens
        pointer_n += 1
    return Packed(
        out^,
        tokens,
        budget,
        counter.method.copy(),
        whole_n,
        pointer_n,
        oversized,
        len(results) - whole_n - pointer_n,
    )
