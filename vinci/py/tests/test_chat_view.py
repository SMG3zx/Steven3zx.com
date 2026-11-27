"""What the chat shows: the tool-call line a person reads, and a failed turn leaving the history as it was."""

import pytest

from vinci_core import chat as chat_mod
from vinci_core.chat import Chat, DIM, paint
from vinci_core.chat_action import SUMMARY_CHARS, parse_action, plain_summary


def action_of(reply: str):
    parsed = parse_action(reply)
    if parsed is None:
        raise AssertionError("expected an action in: " + reply)
    return parsed


def test_journal_call_reads_as_words() -> None:
    a = action_of('{"tool": "journal", "args": ["idea", "Flying machine"]}')
    assert plain_summary(a) == "idea Flying machine"


def test_recall_reads_as_its_query() -> None:
    a = action_of('{"tool":"recall","query":"gliders"}')
    assert plain_summary(a) == "gliders"


def test_later_arguments_keep_their_names() -> None:
    a = action_of('{"tool":"recall","query":"gliders","sources":"git,vaults"}')
    assert plain_summary(a) == "gliders sources=git,vaults"


def test_empty_arguments_are_left_out() -> None:
    a = action_of('{"tool":"recall","query":"","sources":"git"}')
    assert plain_summary(a) == "sources=git"


def test_long_summary_is_cut_with_an_ellipsis() -> None:
    a = action_of('{"tool":"recall","query":"' + "x" * 300 + '"}')
    text = plain_summary(a)
    assert text.endswith("…")
    assert len(text.encode("utf-8")) <= SUMMARY_CHARS + len("…".encode("utf-8"))


class _Tools:
    """Stands in for ChatTools: no upfront recall, nothing to update."""

    def upfront_recall(self, _text: str) -> str:
        return ""

    def index_update(self) -> None:
        pass


def _chat() -> Chat:
    return Chat(_Tools(), "http://unused", "qwen3.5-9b", False, 24000, "vinci-chat-test", None, "", "")  # type: ignore[arg-type]


def test_failed_turn_leaves_no_history_behind() -> None:
    chat = _chat()
    chat.history.append(chat_mod.Message("user", "earlier question"))
    chat.history.append(chat_mod.Message("assistant", "earlier answer"))

    def down() -> str:
        raise ValueError(chat_mod.CHAT_MODEL_ERROR + "connection refused")

    chat.ask = down  # type: ignore[method-assign]
    with pytest.raises(ValueError):
        chat.turn("a new question")
    assert [m.content for m in chat.history] == ["earlier question", "earlier answer"]


def test_paint_is_plain_when_output_is_not_a_terminal() -> None:
    # Under pytest, standard output is captured, so it is not a terminal.
    assert paint("tool line", DIM) == "tool line"
