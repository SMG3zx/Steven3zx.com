"""The write confirmation's decisions: which tool calls write, when a write needs a yes, and what
counts as a yes. The prompt itself reads standard input, so only the decisions are tested."""

from vinci_core.command_kinds import chat_runs
from vinci_core.chat_gate import (
    is_read_only_command,
    is_write_call,
    is_yes,
    needs_confirmation,
    refusal,
)


def test_journal_writes() -> None:
    assert not is_write_call("journal", ["ideas"])
    assert not is_write_call("journal", ["show"])
    assert not is_write_call("journal", [])
    assert is_write_call("journal", ["idea"])
    assert is_write_call("journal", ["review"])
    assert not chat_runs("sync"), "sync is not a command: the tracker runs in every command"
    assert is_write_call("journal", ["note"])
    assert is_read_only_command("pipeline")
    assert not is_read_only_command("pipelines")


def test_other_tools() -> None:
    assert is_write_call("remember", [])
    assert is_write_call("read", [])
    assert not is_write_call("recall", [])
    assert not is_write_call("open", [])
    assert not is_write_call("nosuch", [])


def test_gate_decision() -> None:
    assert not needs_confirmation(False, True)
    assert needs_confirmation(True, True)
    assert not needs_confirmation(True, False)
    assert not needs_confirmation(False, False)


def test_yes_and_no() -> None:
    assert is_yes("y")
    assert is_yes(" YES ")
    assert not is_yes("n")
    assert not is_yes("")
    assert not is_yes("yes please")
    assert refusal().startswith("refused:")
