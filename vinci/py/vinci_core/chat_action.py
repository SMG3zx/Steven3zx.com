"""A chat reply that asks for a tool.

A reply that is one JSON object with a "tool" name is an action; anything else is the answer. The
arguments are kept in order, with each value as a string, a list of strings, or other JSON text, so
the summary shown to the person keeps the model's own order.

Port of mojo/src/chat_action.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional

from .jsonline import Cursor, ascii_lower, json_string

VALUE_STRING = 0
VALUE_LIST = 1
VALUE_OTHER = 2
SUMMARY_CHARS = 90
BACKTICK = 96
OPEN_BRACE = 123
CLOSE_BRACE = 125
QUOTE = 34


@dataclass
class ArgValue:
    """One argument. text is the string (VALUE_STRING) or the raw JSON (VALUE_OTHER). items holds
    the strings of a VALUE_LIST."""

    key: str
    kind: int
    text: str = ""
    items: list[str] = field(default_factory=list)


@dataclass
class Action:
    """The tool name and the arguments, without the "tool" key itself."""

    name: str
    values: list[ArgValue]


def is_ascii_space(c: int) -> bool:
    return c in (32, 9, 10, 13)


def trim_bytes(text: str, strip_backticks: bool) -> str:
    """Removes white space, and backticks when asked, from both ends."""
    b = text.encode("utf-8")
    start = 0
    end = len(b)
    while start < end and (is_ascii_space(b[start]) or (strip_backticks and b[start] == BACKTICK)):
        start += 1
    while end > start and (is_ascii_space(b[end - 1]) or (strip_backticks and b[end - 1] == BACKTICK)):
        end -= 1
    return b[start:end].decode("utf-8")


def strip_fence(reply: str) -> str:
    """A reply in a code fence: the backticks go, and a json tag after them. As TryAction."""
    text = trim_bytes(reply, False)
    if not text.startswith("```"):
        return text
    text = trim_bytes(text, True)
    if ascii_lower(text).startswith("json"):
        text = trim_bytes(text.encode("utf-8")[4:].decode("utf-8"), False)
    return text


def raw_slice(c: Cursor, start: int, end: int) -> str:
    return c.data[start:end].decode("utf-8")


def parse_members(c: Cursor, values: list[ArgValue]) -> None:
    """The members of one JSON object, after its opening brace, up to and including the closing
    brace."""
    while True:
        c.skip_space()
        if c.peek() == CLOSE_BRACE:
            c.take()
            return
        key = c.read_string()
        c.skip_space()
        c.expect(58)  # :
        c.skip_space()
        if c.peek() == QUOTE:
            values.append(ArgValue(key, VALUE_STRING, c.read_string(), []))
        elif c.peek() == 91:  # [
            values.append(ArgValue(key, VALUE_LIST, "", c.read_string_array()))
        else:
            start = c.pos
            c.skip_value()
            values.append(ArgValue(key, VALUE_OTHER, raw_slice(c, start, c.pos), []))
        c.skip_space()
        sep = c.take()
        if sep == CLOSE_BRACE:
            return
        if sep != 44:  # ,
            raise ValueError("chat: expected , or } in an action")


def parse_action(reply: str) -> Optional[Action]:
    """The action a reply asks for, or None when the reply is the answer.

    A reply that is one JSON object is the call. A reply with words around a call (the model often
    says what it will do, then prints the call) still makes the call: the first object with a string
    "tool" is run, and the words are dropped. A reply that starts with { but is not one JSON object is
    an answer, as before."""
    text = strip_fence(reply)
    if text.startswith("{"):
        return _action_in(text, whole=True)
    start = text.find("{")
    while start >= 0:
        found = _action_in(text[start:], whole=False)
        if found is not None:
            return found
        start = text.find("{", start + 1)
    return None


def _action_in(text: str, whole: bool) -> Optional[Action]:
    """The action of the JSON object that begins text. whole: the object must be all of text."""
    values: list[ArgValue] = []
    try:
        c = Cursor(text)
        c.skip_space()
        c.expect(OPEN_BRACE)
        parse_members(c, values)
        if whole:
            c.skip_space()
            if not c.at_end():
                return None
    except ValueError:
        return None
    name_at = -1
    for i, v in enumerate(values):
        if v.key == "tool" and v.kind == VALUE_STRING:
            name_at = i
            break
    if name_at < 0:
        return None
    name = values[name_at].text
    rest = [v for i, v in enumerate(values) if i != name_at]
    return Action(name, rest)


def find_value(a: Action, key: str) -> int:
    for i, v in enumerate(a.values):
        if v.key == key:
            return i
    return -1


def has_arg(a: Action, key: str) -> bool:
    return find_value(a, key) >= 0


def text_arg(a: Action, key: str) -> str:
    """A string argument. Missing is an error that names the key; a non-string is one too."""
    at = find_value(a, key)
    if at < 0:
        raise ValueError(key + " is required")
    if a.values[at].kind != VALUE_STRING:
        raise ValueError(key + " must be text")
    return a.values[at].text


def optional_text_arg(a: Action, key: str) -> Optional[str]:
    if not has_arg(a, key):
        return None
    return text_arg(a, key)


def list_arg(a: Action, key: str) -> list[str]:
    """A list of strings. Missing or not a list is an error that names the form."""
    at = find_value(a, key)
    if at < 0 or a.values[at].kind != VALUE_LIST:
        raise ValueError(key + " must be a list of strings")
    return list(a.values[at].items)


def int_arg(a: Action, key: str, fallback: int) -> int:
    """An optional whole number. A string of digits is read; anything else is an error."""
    at = find_value(a, key)
    if at < 0:
        return fallback
    if a.values[at].kind == VALUE_LIST:
        raise ValueError(key + " must be a whole number")
    try:
        return int(a.values[at].text)
    except ValueError:
        raise ValueError(key + " must be a whole number") from None


def compact_json(a: Action) -> str:
    """The arguments as one line of JSON: keys in order, no spaces, the tool name left out."""
    out = "{"
    for i, v in enumerate(a.values):
        if i > 0:
            out += ","
        out += json_string(v.key) + ":"
        if v.kind == VALUE_STRING:
            out += json_string(v.text)
        elif v.kind == VALUE_LIST:
            out += "[" + ",".join(json_string(item) for item in v.items) + "]"
        else:
            out += v.text
    return out + "}"


def plain_summary(a: Action) -> str:
    """The arguments as a person reads them: the first value bare, the others as key=value, lists as
    their items space-separated, cut to SUMMARY_CHARS. recall "gliders" reads as: recall gliders."""
    parts: list[str] = []
    for i, v in enumerate(a.values):
        text = " ".join(v.items) if v.kind == VALUE_LIST else v.text
        if text == "":
            continue
        parts.append(text if i == 0 else v.key + "=" + text)
    out = " ".join(parts)
    if len(out.encode("utf-8")) <= SUMMARY_CHARS:
        return out
    return out.encode("utf-8")[: SUMMARY_CHARS - 1].decode("utf-8", errors="ignore") + "…"


def summary_of(a: Action) -> str:
    """The arguments as JSON, cut to 90 characters with an ellipsis."""
    text = compact_json(a)
    if len(text.encode("utf-8")) <= SUMMARY_CHARS:
        return text
    return text.encode("utf-8")[: SUMMARY_CHARS - 1].decode("utf-8", errors="ignore") + "…"
