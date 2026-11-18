# Session tests: Arg, Number and NextId, against the C# behaviour in src/App.cs.
# The error texts are the ones C# throws as UserError, with the user: marker added.

from std.os import makedirs, remove
from std.pathlib import Path
from std.testing import assert_equal, assert_true

from journal import Event, Journal, JournalTx
from session import Session, collapse_spaces, number
from timeutil import Instant, parse_instant

comptime DIR = "build/test-session"
comptime PATH = "build/test-session/journal.jsonl"


def test_collapse_spaces() raises:
    assert_equal(collapse_spaces("  a \t b  "), "a b")
    assert_equal(collapse_spaces("one\ntwo\r\nthree"), "one two three")
    assert_equal(collapse_spaces("  \t  "), "")
    # Char.IsWhiteSpace also takes NBSP and the ideographic space.
    assert_equal(collapse_spaces("a b　c"), "a b c")
    assert_equal(collapse_spaces("no change"), "no change")


def test_number_accepts() raises:
    assert_equal(number("5", "x"), 5.0)
    assert_equal(number("-2.5", "x"), -2.5)
    assert_equal(number("+4", "x"), 4.0)
    assert_equal(number(".5", "x"), 0.5)
    assert_equal(number("5.", "x"), 5.0)
    assert_equal(number("1e3", "x"), 1000.0)
    assert_equal(number("-.5e+3", "x"), -500.0)
    assert_equal(number(" 7 ", "x"), 7.0)


def test_number_refuses() raises:
    var texts = List[String]()
    texts.append("")
    texts.append("abc")
    texts.append(".")
    texts.append("+")
    texts.append("1e")
    texts.append("1,000")
    texts.append("NaN")
    texts.append("Infinity")
    texts.append("1e400")
    for text in texts:
        var raised = False
        try:
            _ = number(text, "value")
        except e:
            raised = True
            assert_equal(
                String(e),
                "user:<value> must be a number, got '" + text + "'",
            )
        assert_true(raised, "expected refusal of '" + text + "'")


def test_arg_through_session() raises:
    var dir = DIR
    makedirs(dir, exist_ok=True)
    if Path(PATH).exists():
        remove(PATH)
    var s = Session(JournalTx(PATH), parse_instant("2026-11-20T12:00:00-06:00"))
    var a = List[String]()
    a.append("  two   words ")
    assert_equal(s.arg(a, 0, "title"), "two words")

    var empty = List[String]()
    var raised = False
    try:
        var s2 = Session(
            JournalTx(PATH), parse_instant("2026-11-20T12:00:00-06:00")
        )
        _ = s2.arg(empty, 0, "title")
    except e:
        raised = True
        assert_equal(String(e), "user:missing <title>")
    assert_true(raised, "expected missing <title>")

    var blank = List[String]()
    blank.append("   ")
    raised = False
    try:
        var s3 = Session(
            JournalTx(PATH), parse_instant("2026-11-20T12:00:00-06:00")
        )
        _ = s3.arg(blank, 0, "title")
    except e:
        raised = True
        assert_equal(String(e), "user:<title> is empty")
    assert_true(raised, "expected <title> is empty")


def test_next_id() raises:
    var dir = DIR
    makedirs(dir, exist_ok=True)
    if Path(PATH).exists():
        remove(PATH)
    var journal = Journal(PATH)
    var s0 = Session(
        JournalTx(PATH), parse_instant("2026-11-20T12:00:00-06:00")
    )
    assert_equal(s0.next_id("i"), "i1")
    assert_equal(s0.next_id("r"), "r1")

    var at = parse_instant("2026-11-20T12:00:00-06:00")
    journal.append(Event(at.copy(), "idea", "i1", "first"))
    journal.append(Event(at.copy(), "idea", "i2", "second"))
    var s = Session(JournalTx(PATH), parse_instant("2026-11-20T12:00:00-06:00"))
    assert_equal(s.next_id("i"), "i3")
    assert_equal(s.next_id("r"), "r1")
    remove(PATH)


def main() raises:
    test_collapse_spaces()
    print("test_collapse_spaces: ok")
    test_number_accepts()
    print("test_number_accepts: ok")
    test_number_refuses()
    print("test_number_refuses: ok")
    test_arg_through_session()
    print("test_arg_through_session: ok")
    test_next_id()
    print("test_next_id: ok")
