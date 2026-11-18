# The write confirmation's decisions: which tool calls write, when a write needs a yes, and
# what counts as a yes. The prompt itself reads standard input, so only the decisions are tested.

from std.testing import assert_equal, assert_false, assert_true

from chat_gate import (
    is_read_only_command,
    is_write_call,
    is_yes,
    needs_confirmation,
    refusal,
)


def one(word: String) -> List[String]:
    var args = List[String]()
    args.append(word)
    return args^


def test_journal_writes() raises:
    assert_false(is_write_call("journal", one("ideas")))
    assert_false(is_write_call("journal", one("show")))
    assert_false(is_write_call("journal", List[String]()))
    assert_true(is_write_call("journal", one("idea")))
    assert_true(is_write_call("journal", one("review")))
    assert_true(is_write_call("journal", one("sync")))
    assert_true(is_write_call("journal", one("note")))
    assert_true(is_read_only_command("pipeline"))
    assert_false(is_read_only_command("pipelines"))
    print("test_journal_writes: ok")


def test_other_tools() raises:
    assert_true(is_write_call("remember", List[String]()))
    assert_true(is_write_call("read", List[String]()))
    assert_false(is_write_call("recall", List[String]()))
    assert_false(is_write_call("open", List[String]()))
    assert_false(is_write_call("nosuch", List[String]()))
    print("test_other_tools: ok")


def test_gate_decision() raises:
    assert_false(needs_confirmation(False, True))
    assert_true(needs_confirmation(True, True))
    assert_false(needs_confirmation(True, False))
    assert_false(needs_confirmation(False, False))
    print("test_gate_decision: ok")


def test_yes_and_no() raises:
    assert_true(is_yes("y"))
    assert_true(is_yes(" YES "))
    assert_false(is_yes("n"))
    assert_false(is_yes(""))
    assert_false(is_yes("yes please"))
    assert_true(refusal().startswith("refused:"))
    print("test_yes_and_no: ok")


def main() raises:
    test_journal_writes()
    test_other_tools()
    test_gate_decision()
    test_yes_and_no()
    print("test_chat_gate: ok")
