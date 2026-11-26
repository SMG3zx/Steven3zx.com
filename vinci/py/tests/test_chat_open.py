"""The open tool's output in the chat: one call's total is capped at 4000 bytes, the notice says
what to read on with, and an id with many facts (i1 has 30) is cut rather than printed whole."""

from vinci_core.chat_open import OPEN_TOTAL_BYTES, cap_output, open_text
from vinci_core.handles import Stored
from vinci_core.open_view import Request


def repeated(word: str, count: int) -> str:
    return "".join(word + " " + str(i) + "\n" for i in range(count))


def fact(text: str) -> Stored:
    return Stored("idea", "2026-11-16T12:00:00+00:00", text, "")


def test_short_output_is_not_cut() -> None:
    text = "[i1] idea 2026-11-16 (2 lines)\n1: one\n2: two"
    assert cap_output(text, "i1", False) == text


def test_long_output_is_cut_with_notice() -> None:
    out = cap_output(repeated("wing rib line", 600), "d1", False)
    assert len(out.encode("utf-8")) <= OPEN_TOTAL_BYTES
    assert "output cut at 4000 bytes" in out
    assert "read on with open d1 grep=<word> or open d1 lines=A-B" in out
    assert "bytes not shown" in out


def test_multi_fact_notice_has_no_line_range() -> None:
    out = cap_output(repeated("fact line", 800), "i1", True)
    assert len(out.encode("utf-8")) <= OPEN_TOTAL_BYTES
    assert "grep=<word>" in out
    assert "lines=A-B" not in out


def test_thirty_facts_are_capped() -> None:
    items = [fact("fact " + str(i) + " " + repeated("detail", 40)) for i in range(30)]
    out = open_text(items, "i1", Request())
    assert len(out.encode("utf-8")) <= OPEN_TOTAL_BYTES
    assert "output cut at 4000 bytes" in out
    assert "[i1] idea" in out


def test_grep_takes_matching_lines() -> None:
    items = [fact("alpha\nthe wing ribs\nbeta\ngamma\ndelta")]
    out = open_text(items, "d1", Request(False, 0, 0, True, "wing"))
    assert "the wing ribs" in out
    assert "gamma" not in out
