"""Tool argument parsing: what a reply must look like to be an action (a JSON object with a "tool"
name, optionally in a code fence), the typed accessors the tools use, and the summary the chat
prints for each call."""

import pytest

from vinci_core.chat_action import Action, int_arg, list_arg, parse_action, summary_of, text_arg


def action_of(reply: str) -> Action:
    parsed = parse_action(reply)
    if parsed is None:
        raise AssertionError("expected an action in: " + reply)
    return parsed


def test_journal_action() -> None:
    a = action_of('{"tool": "journal", "args": ["idea", "Flying machine"]}')
    assert a.name == "journal"
    args = list_arg(a, "args")
    assert len(args) == 2
    assert args[1] == "Flying machine"
    assert summary_of(a) == '{"args":["idea","Flying machine"]}'


def test_fenced_action() -> None:
    a = action_of('```json\n{"tool":"recall","query":"gliders"}\n```')
    assert a.name == "recall"
    assert text_arg(a, "query") == "gliders"


@pytest.mark.parametrize(
    "reply",
    [
        "Plain answer.",
        '{"query": "no tool here"}',
        '{"tool": "recall"} trailing',
        '{"tool": 3}',
        '{"tool": "recall", "query": ',
    ],
)
def test_answers_are_not_actions(reply: str) -> None:
    assert parse_action(reply) is None


def test_accessors() -> None:
    a = action_of('{"tool": "open", "id": "i1", "budget": 500}')
    assert text_arg(a, "id") == "i1"
    assert int_arg(a, "budget", 3000) == 500
    assert int_arg(a, "whole", 10) == 10
    with pytest.raises(ValueError, match="^grep is required$"):
        text_arg(a, "grep")
    with pytest.raises(ValueError, match="^id must be a list of strings$"):
        list_arg(a, "id")


def test_summary_is_cut() -> None:
    a = action_of('{"tool": "remember", "content": "' + "x" * 200 + '"}')
    text = summary_of(a)
    assert text.endswith("…")
    assert len(text.encode("utf-8")) <= 92
