"""Session tests: Arg, Number and NextId, against the C# behaviour in src/App.cs. The error texts
are the ones C# throws as UserError."""

import pytest

from vinci_core.journal import Event, Journal, JournalTx
from vinci_core.session import Session, UserError, collapse_spaces, number
from vinci_core.timeutil import parse_instant

NOW = "2026-11-20T12:00:00-06:00"


def test_collapse_spaces() -> None:
    assert collapse_spaces("  a \t b  ") == "a b"
    assert collapse_spaces("one\ntwo\r\nthree") == "one two three"
    assert collapse_spaces("  \t  ") == ""
    # Char.IsWhiteSpace also takes NBSP and the ideographic space.
    assert collapse_spaces("a b　c") == "a b c"
    assert collapse_spaces("no change") == "no change"


def test_number_accepts() -> None:
    assert number("5", "x") == 5.0
    assert number("-2.5", "x") == -2.5
    assert number("+4", "x") == 4.0
    assert number(".5", "x") == 0.5
    assert number("5.", "x") == 5.0
    assert number("1e3", "x") == 1000.0
    assert number("-.5e+3", "x") == -500.0
    assert number(" 7 ", "x") == 7.0


@pytest.mark.parametrize("text", ["", "abc", ".", "+", "1e", "1,000", "NaN", "Infinity", "1e400"])
def test_number_refuses(text: str) -> None:
    with pytest.raises(UserError) as info:
        number(text, "value")
    assert str(info.value) == "<value> must be a number, got '" + text + "'"


def test_arg_through_session(tmp_path) -> None:  # type: ignore[no-untyped-def]
    path = str(tmp_path / "journal.jsonl")
    s = Session(JournalTx(path), parse_instant(NOW))
    assert s.arg(["  two   words "], 0, "title") == "two words"
    with pytest.raises(UserError) as missing:
        Session(JournalTx(path), parse_instant(NOW)).arg([], 0, "title")
    assert str(missing.value) == "missing <title>"
    with pytest.raises(UserError) as empty:
        Session(JournalTx(path), parse_instant(NOW)).arg(["   "], 0, "title")
    assert str(empty.value) == "<title> is empty"


def test_next_id(tmp_path) -> None:  # type: ignore[no-untyped-def]
    path = str(tmp_path / "journal.jsonl")
    s0 = Session(JournalTx(path), parse_instant(NOW))
    assert s0.next_id("i") == "i1"
    assert s0.next_id("r") == "r1"
    at = parse_instant(NOW)
    journal = Journal(path)
    journal.append(Event(at, "idea", id="i1", text="first"))
    journal.append(Event(at, "idea", id="i2", text="second"))
    s = Session(JournalTx(path), parse_instant(NOW))
    assert s.next_id("i") == "i3"
    assert s.next_id("r") == "r1"
