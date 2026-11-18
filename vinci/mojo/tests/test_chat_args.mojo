# Tool argument parsing: what a reply must look like to be an action (a JSON object with a
# "tool" name, optionally in a code fence), the typed accessors the tools use, and the summary
# the chat prints for each call.

from std.testing import assert_equal, assert_false, assert_true

from chat_action import (
    Action,
    int_arg,
    list_arg,
    parse_action,
    summary_of,
    text_arg,
)


def action_of(reply: String) raises -> Action:
    var parsed = parse_action(reply)
    if parsed is None:
        raise Error("expected an action in: " + reply)
    return parsed.value().copy()


def test_journal_action() raises:
    var a = action_of('{"tool": "journal", "args": ["idea", "Flying machine"]}')
    assert_equal(a.name, "journal")
    var args = list_arg(a, "args")
    assert_equal(len(args), 2)
    assert_equal(args[1], "Flying machine")
    assert_equal(summary_of(a), '{"args":["idea","Flying machine"]}')
    print("test_journal_action: ok")


def test_fenced_action() raises:
    var a = action_of('```json\n{"tool":"recall","query":"gliders"}\n```')
    assert_equal(a.name, "recall")
    assert_equal(text_arg(a, "query"), "gliders")
    print("test_fenced_action: ok")


def test_answers_are_not_actions() raises:
    assert_false(parse_action("Plain answer.") is not None)
    assert_false(parse_action('{"query": "no tool here"}') is not None)
    assert_false(parse_action('{"tool": "recall"} trailing') is not None)
    assert_false(parse_action('{"tool": 3}') is not None)
    assert_false(parse_action('{"tool": "recall", "query": ') is not None)
    print("test_answers_are_not_actions: ok")


def test_accessors() raises:
    var a = action_of('{"tool": "open", "id": "i1", "budget": 500}')
    assert_equal(text_arg(a, "id"), "i1")
    assert_equal(int_arg(a, "budget", 3000), 500)
    assert_equal(int_arg(a, "whole", 10), 10)
    var missing = False
    try:
        _ = text_arg(a, "grep")
    except e:
        missing = String(e) == "grep is required"
    assert_true(missing)
    var not_list = False
    try:
        _ = list_arg(a, "id")
    except e:
        not_list = String(e) == "id must be a list of strings"
    assert_true(not_list)
    print("test_accessors: ok")


def test_summary_is_cut() raises:
    var long = "x" * 200
    var a = action_of('{"tool": "remember", "content": "' + long + '"}')
    var text = summary_of(a)
    assert_true(text.endswith("…"))
    assert_true(text.byte_length() <= 92)
    print("test_summary_is_cut: ok")


def main() raises:
    test_journal_action()
    test_fenced_action()
    test_answers_are_not_actions()
    test_accessors()
    test_summary_is_cut()
    print("test_chat_args: ok")
