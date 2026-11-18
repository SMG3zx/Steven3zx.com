# The open tool's output in the chat: one call's total is capped at 4000 bytes, the notice says
# what to read on with, and an id with many facts (i1 has 30) is cut rather than printed whole.

from std.testing import assert_equal, assert_false, assert_true

from chat_open import OPEN_TOTAL_BYTES, cap_output, open_text
from handles import Stored
from open_view import Request, no_request


def repeated(word: String, count: Int) -> String:
    var out = String()
    for i in range(count):
        out += word + " " + String(i) + "\n"
    return out


def fact(text: String) -> Stored:
    return Stored("idea", "2026-11-16T12:00:00+00:00", text, "")


def test_short_output_is_not_cut() raises:
    var text = String("[i1] idea 2026-11-16 (2 lines)\n1: one\n2: two")
    assert_equal(cap_output(text, "i1", False), text)
    print("test_short_output_is_not_cut: ok")


def test_long_output_is_cut_with_notice() raises:
    var text = repeated("wing rib line", 600)
    var out = cap_output(text, "d1", False)
    assert_true(out.byte_length() <= OPEN_TOTAL_BYTES)
    assert_true(out.find("output cut at 4000 bytes") >= 0)
    assert_true(
        out.find("read on with open d1 grep=<word> or open d1 lines=A-B") >= 0
    )
    assert_true(out.find("bytes not shown") >= 0)
    print("test_long_output_is_cut_with_notice: ok")


def test_multi_fact_notice_has_no_line_range() raises:
    var text = repeated("fact line", 800)
    var out = cap_output(text, "i1", True)
    assert_true(out.byte_length() <= OPEN_TOTAL_BYTES)
    assert_true(out.find("grep=<word>") >= 0)
    assert_false(out.find("lines=A-B") >= 0)
    print("test_multi_fact_notice_has_no_line_range: ok")


def test_thirty_facts_are_capped() raises:
    var items = List[Stored]()
    for i in range(30):
        items.append(fact("fact " + String(i) + " " + repeated("detail", 40)))
    var req = no_request()
    var out = open_text(items, "i1", req)
    assert_true(out.byte_length() <= OPEN_TOTAL_BYTES)
    assert_true(out.find("output cut at 4000 bytes") >= 0)
    assert_true(out.find("[i1] idea") >= 0)
    print("test_thirty_facts_are_capped: ok")


def test_grep_takes_matching_lines() raises:
    var items = List[Stored]()
    items.append(fact("alpha\nthe wing ribs\nbeta\ngamma\ndelta"))
    var req = Request(False, 0, 0, True, "wing")
    var out = open_text(items, "d1", req)
    assert_true(out.find("the wing ribs") >= 0)
    assert_false(out.find("gamma") >= 0)
    print("test_grep_takes_matching_lines: ok")


def main() raises:
    test_short_output_is_not_cut()
    test_long_output_is_cut_with_notice()
    test_multi_fact_notice_has_no_line_range()
    test_thirty_facts_are_capped()
    test_grep_takes_matching_lines()
    print("test_chat_open: ok")
