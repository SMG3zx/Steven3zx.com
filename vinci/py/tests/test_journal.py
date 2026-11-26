"""Journal tests. The expected escapes and numbers were produced by a probe that serialized the same
Event with System.Text.Json (JsonSerializerDefaults.Web, WhenWritingNull) on .NET 10."""

from pathlib import Path

import pytest

from vinci_core.journal import Event, Journal, parse_event, serialize_event
from vinci_core.jsonline import format_json_double, json_string
from vinci_core.timeutil import parse_instant


def test_json_string_escapes() -> None:
    assert json_string("plain") == '"plain"'
    assert json_string('quo"te') == '"quo\\u0022te"'
    assert json_string("back\\slash") == '"back\\\\slash"'
    assert json_string("ge≥ and é") == '"ge\\u2265 and \\u00E9"'
    assert json_string("emoji 😀 pair") == '"emoji \\uD83D\\uDE00 pair"'
    assert json_string("<tag>") == '"\\u003Ctag\\u003E"'
    assert json_string("a+b") == '"a\\u002Bb"'
    assert json_string("tab\there") == '"tab\\there"'
    assert json_string("new\nline") == '"new\\nline"'
    assert json_string("cr\rx") == '"cr\\rx"'
    assert json_string("it's") == '"it\\u0027s"'
    assert json_string("back`tick") == '"back\\u0060tick"'
    assert json_string("amp&ersand") == '"amp\\u0026ersand"'
    assert json_string("\u0001\u001f") == '"\\u0001\\u001F"'
    assert json_string("\u0008ff\u000c") == '"\\bff\\f"'
    assert json_string("del\u007f") == '"del\\u007F"'
    assert json_string(" sep") == '"\\u2028sep"'
    assert json_string("qé中") == '"q\\u00E9\\u4E2D"'
    assert json_string("😀🚀") == '"\\uD83D\\uDE00\\uD83D\\uDE80"'
    assert json_string("ASCII ~ tilde [] {} | / ok") == '"ASCII ~ tilde [] {} | / ok"'


@pytest.mark.parametrize(
    "value, want",
    [
        (0.1, "0.1"), (1.0 / 3.0, "0.3333333333333333"), (100.0, "100"), (1e-7, "1E-07"),
        (123456789.123, "123456789.123"), (5.0, "5"), (0.25, "0.25"), (64.06, "64.06"),
        (1e21, "1E+21"), (1e15, "1000000000000000"), (1e16, "10000000000000000"),
        (1e17, "1E+17"), (1e-4, "0.0001"), (1e-5, "1E-05"), (0.0001234, "0.0001234"),
        (1.5e300, "1.5E+300"), (5e-324, "5E-324"), (-0.0, "-0"), (2.675, "2.675"),
        (59.9999, "59.9999"), (16.18, "16.18"), (-2.5, "-2.5"),
        (9007199254740993.0, "9007199254740992"), (12345678901234567.0, "12345678901234568"),
        (1.2345678901234568e20, "1.2345678901234568E+20"),
    ],
)
def test_json_double(value: float, want: str) -> None:
    assert format_json_double(value) == want


def test_serialize_layout() -> None:
    at = parse_instant("2026-11-15T10:25:31.3748456-06:00")
    assert serialize_event(Event(at, "idea", id="i1", text="vinci")) == (
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"idea","id":"i1","text":"vinci"}'
    )
    assert serialize_event(Event(at, "link", id="n1", refs=["i1", "s2"], tags=["x", "y≥"])) == (
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"link","id":"n1","refs":["i1","s2"],'
        '"tags":["x","y\\u2265"]}'
    )
    assert serialize_event(Event(at, "link", id="n1", refs=[])) == (
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"link","id":"n1","refs":[]}'
    )
    # Nulls are left out, and a zero is written.
    assert serialize_event(Event(at, "x", hi=0.0)) == (
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"x","hi":0}'
    )
    # Declaration order, whatever order the fields are set in.
    full = Event(
        at, "k", id="id", text="t", value=1.5, hi=2.5, who="w", why="y", url="u", metric="m", where_="where"
    )
    assert serialize_event(full) == (
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"k","id":"id","text":"t","value":1.5,'
        '"hi":2.5,"who":"w","why":"y","url":"u","metric":"m","where":"where"}'
    )


def test_serialize_timestamps() -> None:
    assert serialize_event(Event(parse_instant("2026-11-17T02:53:53.9943206+00:00"), "k")) == (
        '{"at":"2026-11-17T02:53:53.9943206+00:00","kind":"k"}'
    )
    assert serialize_event(Event(parse_instant("2026-11-15T09:49:01Z"), "k")) == (
        '{"at":"2026-11-15T09:49:01+00:00","kind":"k"}'
    )
    assert serialize_event(Event(parse_instant("2026-11-15T09:49:01.5+05:30"), "k")) == (
        '{"at":"2026-11-15T09:49:01.5+05:30","kind":"k"}'
    )


def test_parse_fields() -> None:
    e = parse_event('{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"idea","id":"i1","text":"vinci"}')
    assert e.kind == "idea"
    assert e.id == "i1"
    assert e.text == "vinci"
    assert e.value is None
    assert e.refs is None

    # Keys match whatever their case, as the C# reader does.
    upper = parse_event('{"AT":"2026-11-15T09:49:01-06:00","Kind":"spent","ID":"i1","Value":2.5}')
    assert upper.kind == "spent"
    assert upper.id == "i1"
    assert upper.value == 2.5

    # Nulls are the same as absent fields, and unknown fields of any shape are skipped.
    nulls = parse_event(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","id":null,"value":null,'
        '"extra":{"a":[1,"}",{"b":null}],"c":"]"},"more":true,"n":-1.5e3}'
    )
    assert nulls.id is None
    assert nulls.value is None

    # Escapes: ', a surrogate pair, and the short forms.
    escaped = parse_event(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","text":"\\u0027\\ud83d\\ude00 a\\/b\\tc\\"q\\\\"}'
    )
    assert escaped.text == "'😀 a/b\tc\"q\\"

    lists = parse_event('{"at":"2026-11-15T09:49:01-06:00","kind":"note","refs":["i1","s2"],"tags":[]}')
    assert len(lists.refs or []) == 2
    assert (lists.refs or [])[1] == "s2"
    assert len(lists.tags or []) == 0

    exp = parse_event('{"at":"2026-11-15T09:49:01-06:00","kind":"log","value":1E-07,"hi":-0.25}')
    assert exp.value == 1e-7
    assert exp.hi == -0.25

    zero = parse_event('{"at":"2026-11-15T09:49:01-06:00","kind":"log","value":0}')
    assert zero.value == 0.0


@pytest.mark.parametrize(
    "line",
    [
        '{"kind":"x"}',
        '{"at":null,"kind":"x"}',
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x"} junk',
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","value":"5"}',
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","text":"\\ud83d"}',
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","text":"\\q"}',
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x",}',
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","value":1.}',
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","text":"tab\traw"}',
    ],
)
def test_parse_refusals(line: str) -> None:
    with pytest.raises(ValueError):
        parse_event(line)


def test_parse_serialize_round_trip() -> None:
    """A line C# wrote, with every escape kind, comes back byte for byte."""
    lines = [
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"idea","id":"i1","text":"quo\\u0022te'
        ' \\u2265 \\u003Ctag\\u003E it\\u0027s \\uD83D\\uDE00 \\n\\t\\\\"}',
        '{"at":"2026-11-15T09:49:01-06:00","kind":"spent","id":"i1","value":1E-07}',
        '{"at":"2026-11-15T09:49:01.5+05:30","kind":"stage","id":"i1","text":"Build","value":0.25}',
    ]
    for line in lines:
        assert serialize_event(parse_event(line)) == line


def test_append_and_read(tmp_path: Path) -> None:
    path = str(tmp_path / "nested" / "append.jsonl")
    journal = Journal(path)
    at = parse_instant("2026-11-15T09:49:01-06:00")
    journal.append(Event(at, "idea", id="i1", text="alpha"))
    journal.append(Event(at, "spent", id="i1", value=5.5))
    events = journal.read()
    assert len(events) == 2
    assert events[0].text == "alpha"
    assert events[1].value == 5.5
    text = Path(path).read_bytes()
    assert len(text) == 2 + len(serialize_event(events[0]).encode()) + len(serialize_event(events[1]).encode())
