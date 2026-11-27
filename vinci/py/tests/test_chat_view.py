"""What the chat shows: the tool-call line a person reads, and a failed turn leaving the history as it was."""

import pytest

from vinci_core import chat as chat_mod
from vinci_core.chat import Chat, DIM, paint
from vinci_core.chat_action import SUMMARY_CHARS, parse_action, plain_summary
from vinci_core.chat import bracket_ideas, claims_a_write, system_prompt


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


def test_failed_turn_leaves_no_history_behind(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(chat_mod, "ideas_for_prompt", lambda _path, _now: "")
    chat = _chat()
    chat.history.append(chat_mod.Message("user", "earlier question"))
    chat.history.append(chat_mod.Message("assistant", "earlier answer"))

    def down() -> str:
        raise ValueError(chat_mod.CHAT_MODEL_ERROR + "connection refused")

    chat.ask = down  # type: ignore[method-assign]
    with pytest.raises(ValueError):
        chat.turn("a new question")
    assert [m.content for m in chat.history] == ["earlier question", "earlier answer"]


def test_call_printed_after_words_still_runs() -> None:
    reply = 'I will create the idea now.\n{"tool": "journal", "args": ["idea", "test1"]}'
    a = action_of(reply)
    assert a.name == "journal"
    assert plain_summary(a) == "idea test1"


def test_answer_with_no_call_is_not_an_action() -> None:
    assert parse_action("You have two ideas: vinci and janus.") is None


def test_brace_that_is_not_a_call_is_an_answer() -> None:
    assert parse_action('Use the form {"title": "x"} to name it.') is None


def test_ideas_are_bracketed_for_citation() -> None:
    listing = "i1   Seed     touch 4h45m   age 11d13h  flow 1.7%   vinci\ni2   Retired  touch 0m\n\n"
    assert bracket_ideas(listing) == (
        "[i1] Seed     touch 4h45m   age 11d13h  flow 1.7%   vinci\n[i2] Retired  touch 0m"
    )


def test_prompt_has_the_no_false_claims_rule() -> None:
    assert "Never say that you created" in system_prompt("2026-11-26")


def test_paint_is_plain_when_output_is_not_a_terminal() -> None:
    # Under pytest, standard output is captured, so it is not a terminal.
    assert paint("tool line", DIM) == "tool line"


@pytest.mark.parametrize(
    "answer",
    [
        "I've added details to test2.",
        "I have created the idea test1.",
        "I had saved your note.",
        "I've now moved it to Prepare.",
    ],
)
def test_a_claimed_change_is_caught(answer: str) -> None:
    assert claims_a_write(answer)


@pytest.mark.parametrize(
    "answer",
    [
        "You have two ideas: vinci and janus.",
        "I do not know the list of your projects.",
        "I can create it if you like.",
        "The idea was created on the 12th.",
    ],
)
def test_ordinary_answers_are_not_claims(answer: str) -> None:
    assert not claims_a_write(answer)
