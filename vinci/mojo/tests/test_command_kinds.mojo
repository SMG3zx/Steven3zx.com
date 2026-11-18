# The command table (src/command_kinds.mojo) against the dispatch sources. Every word the
# dispatch tables compare with is in the table, and every table word is compared somewhere, so a
# new command cannot ship unclassified. The chat's decisions (what it may run, what writes) are
# checked on the table's own answers.
#
# Runs from mojo/ (make test), so the sources are read from src/.

from std.pathlib import Path
from std.testing import assert_equal, assert_false, assert_true

from chat_gate import is_write_call
from text import ascii_tail
from timeutil import ascii_prefix
from command_kinds import (
    chat_runs,
    command_table,
    command_writes_journal,
    find_command,
    journal_command_words,
)


def journal_sources() -> List[String]:
    var out = List[String]()
    out.append("src/cmd_ideas.mojo")
    out.append("src/cmd_knowledge.mojo")
    out.append("src/cmd_ops.mojo")
    return out^


def route_sources() -> List[String]:
    var out = List[String]()
    out.append("src/vinci.mojo")
    return out^


def quoted_after(text: String, marker: String) raises -> List[String]:
    """Every word written as marker + '"word"' in text, in order, without repeats.
    """
    var found = List[String]()
    var rest = String(text)
    while True:
        var at = rest.find(marker)
        if at < 0:
            break
        var tail = ascii_tail(rest, at + marker.byte_length())
        var end = tail.find('"')
        if end < 0:
            break
        var word = ascii_prefix(tail, end)
        var seen = False
        for f in found:
            if f == word:
                seen = True
        if not seen and word.byte_length() > 0:
            found.append(word)
        rest = ascii_tail(tail, end + 1)
    return found^


def dispatch_words() raises -> List[String]:
    """The journal words the dispatch functions compare with: cmd == "word"."""
    var words = List[String]()
    for path in journal_sources():
        var text = Path(path).read_text()
        for w in quoted_after(text, 'cmd == "'):
            words.append(w.copy())
    return words^


def route_words() raises -> List[String]:
    """The words vinci.mojo routes before the journal: args[0] == "word"."""
    var words = List[String]()
    for path in route_sources():
        var text = Path(path).read_text()
        for w in quoted_after(text, 'args[0] == "'):
            words.append(w.copy())
    return words^


def contains(words: List[String], word: String) -> Bool:
    for w in words:
        if w == word:
            return True
    return False


def test_every_dispatched_word_is_classified() raises:
    var journal_seen = dispatch_words()
    assert_true(
        len(journal_seen) > 40, "the dispatch sources yielded too few words"
    )
    for w in journal_seen:
        assert_true(
            find_command(w) >= 0,
            "dispatch word '" + w + "' is not in command_kinds.mojo",
        )
    var routed = route_words()
    for w in routed:
        assert_true(
            find_command(w) >= 0,
            "routed word '" + w + "' is not in command_kinds.mojo",
        )


def test_every_table_word_is_dispatched() raises:
    """A table word that no dispatch compares with is stale: the table would then allow a
    command that does not exist."""
    var journal_seen = dispatch_words()
    var routed = route_words()
    for w in journal_command_words():
        assert_true(
            contains(journal_seen, w),
            "table word '" + w + "' is not compared in a journal dispatch",
        )
    for k in command_table():
        if k.route:
            assert_true(
                contains(routed, k.name) or k.name == "chat",
                "table route '" + k.name + "' is not routed in vinci.mojo",
            )


def test_chat_fence_and_gate() raises:
    # Journal commands the chat may run, and what they write.
    assert_true(chat_runs("idea"))
    assert_true(chat_runs("ideas"))
    assert_true(chat_runs("help"))
    assert_false(command_writes_journal("ideas"))
    assert_false(command_writes_journal("show"))
    assert_true(command_writes_journal("idea"))
    assert_true(command_writes_journal("sync"))
    assert_true(command_writes_journal("review"))
    # Routes and unknown words are refused before anything runs.
    for w in [
        "chat",
        "serve",
        "index",
        "feed",
        "recall",
        "open",
        "memory",
        "backup",
        "verify",
    ]:
        assert_false(chat_runs(w), "the chat must refuse '" + w + "'")
    assert_false(chat_runs("nosuch"))
    assert_true(command_writes_journal("nosuch"), "an unknown word is a write")
    # The gate asks for the journal writes and nothing else.
    var one = List[String]()
    one.append("ideas")
    assert_false(is_write_call("journal", one))
    one[0] = "spent"
    assert_true(is_write_call("journal", one))


def test_writes_match_the_old_chat_list() raises:
    """The read-only words the chat allowed before the table existed. They must all still be
    read-only, so the gate does not start asking about them."""
    for w in [
        "help",
        "--help",
        "-h",
        "ideas",
        "pipeline",
        "next",
        "show",
        "notes",
        "tags",
        "sources",
        "predictions",
        "stats",
        "issues",
        "principles",
        "bookmarks",
        "watches",
        "path",
    ]:
        assert_false(command_writes_journal(w), "'" + w + "' must only read")


def main() raises:
    test_every_dispatched_word_is_classified()
    test_every_table_word_is_dispatched()
    test_chat_fence_and_gate()
    test_writes_match_the_old_chat_list()
    print("test_command_kinds: ok")
