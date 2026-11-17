# Journal tests. The expected escapes and numbers were produced by a probe that
# serialized the same Event with System.Text.Json (JsonSerializerDefaults.Web,
# WhenWritingNull) on .NET 10. Run with a journal path as the first argument to
# also round-trip every line of that file (see round_trip below).

from std.os import makedirs, remove
from std.pathlib import Path
from std.sys import argv
from std.testing import assert_equal, assert_true

from journal import Event, Journal, parse_event, serialize_event
from jsonline import format_json_double, json_string
from timeutil import Instant, parse_instant


def assert_parse_refused(line: String) raises:
    var raised = False
    try:
        _ = parse_event(line)
    except:
        raised = True
    assert_true(raised, "expected the reader to refuse: " + line)


def test_json_string_escapes() raises:
    # Expected values are the probe's output, with the backslashes doubled for Mojo.
    assert_equal(json_string("plain"), '"plain"')
    assert_equal(json_string('quo"te'), '"quo\\u0022te"')
    assert_equal(json_string("back\\slash"), '"back\\\\slash"')
    assert_equal(json_string("ge≥ and é"), '"ge\\u2265 and \\u00E9"')
    assert_equal(json_string("emoji 😀 pair"), '"emoji \\uD83D\\uDE00 pair"')
    assert_equal(json_string("<tag>"), '"\\u003Ctag\\u003E"')
    assert_equal(json_string("a+b"), '"a\\u002Bb"')
    assert_equal(json_string("tab\there"), '"tab\\there"')
    assert_equal(json_string("new\nline"), '"new\\nline"')
    assert_equal(json_string("cr\rx"), '"cr\\rx"')
    assert_equal(json_string("it's"), '"it\\u0027s"')
    assert_equal(json_string("back`tick"), '"back\\u0060tick"')
    assert_equal(json_string("amp&ersand"), '"amp\\u0026ersand"')
    assert_equal(json_string("\u0001\u001f"), '"\\u0001\\u001F"')
    assert_equal(json_string("\u0008ff\u000c"), '"\\bff\\f"')
    assert_equal(json_string("del\u007f"), '"del\\u007F"')
    assert_equal(json_string(" sep"), '"\\u2028sep"')
    assert_equal(json_string("qé中"), '"q\\u00E9\\u4E2D"')
    assert_equal(json_string("😀🚀"), '"\\uD83D\\uDE00\\uD83D\\uDE80"')
    assert_equal(
        json_string("ASCII ~ tilde [] {} | / ok"),
        '"ASCII ~ tilde [] {} | / ok"',
    )
    print("test_json_string_escapes: ok")


def test_json_double() raises:
    assert_equal(format_json_double(0.1), "0.1")
    assert_equal(format_json_double(1.0 / 3.0), "0.3333333333333333")
    assert_equal(format_json_double(100.0), "100")
    assert_equal(format_json_double(1e-7), "1E-07")
    assert_equal(format_json_double(123456789.123), "123456789.123")
    assert_equal(format_json_double(5.0), "5")
    assert_equal(format_json_double(0.25), "0.25")
    assert_equal(format_json_double(64.06), "64.06")
    assert_equal(format_json_double(1e21), "1E+21")
    assert_equal(format_json_double(1e15), "1000000000000000")
    assert_equal(format_json_double(1e16), "10000000000000000")
    assert_equal(format_json_double(1e17), "1E+17")
    assert_equal(format_json_double(1e-4), "0.0001")
    assert_equal(format_json_double(1e-5), "1E-05")
    assert_equal(format_json_double(0.0001234), "0.0001234")
    assert_equal(format_json_double(1.5e300), "1.5E+300")
    assert_equal(format_json_double(5e-324), "5E-324")
    assert_equal(format_json_double(-0.0), "-0")
    assert_equal(format_json_double(2.675), "2.675")
    assert_equal(format_json_double(59.9999), "59.9999")
    assert_equal(format_json_double(16.18), "16.18")
    assert_equal(format_json_double(-2.5), "-2.5")
    assert_equal(format_json_double(9007199254740993.0), "9007199254740992")
    assert_equal(format_json_double(12345678901234567.0), "12345678901234568")
    assert_equal(
        format_json_double(1.2345678901234568e20), "1.2345678901234568E+20"
    )
    print("test_json_double: ok")


def test_serialize_layout() raises:
    var at = parse_instant("2026-11-15T10:25:31.3748456-06:00")
    var line = serialize_event(
        Event(at.copy(), "idea", id=String("i1"), text=String("vinci"))
    )
    assert_equal(
        line,
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"idea","id":"i1","text":"vinci"}',
    )
    var refs = List[String]()
    var empty_refs = List[String]()
    refs.append("i1")
    refs.append("s2")
    var tags = List[String]()
    tags.append("x")
    tags.append("y≥")
    assert_equal(
        serialize_event(
            Event(at.copy(), "link", id=String("n1"), refs=refs^, tags=tags^)
        ),
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"link","id":"n1","refs":["i1","s2"],"tags":["x","y\\u2265"]}',
    )
    assert_equal(
        serialize_event(
            Event(at.copy(), "link", id=String("n1"), refs=empty_refs^)
        ),
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"link","id":"n1","refs":[]}',
    )
    # Nulls are left out, and a zero is written.
    assert_equal(
        serialize_event(Event(at.copy(), "x", hi=Float64(0.0))),
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"x","hi":0}',
    )
    # Declaration order, whatever order the fields are set in.
    assert_equal(
        serialize_event(
            Event(
                at.copy(),
                "k",
                id=String("id"),
                text=String("t"),
                value=Float64(1.5),
                hi=Float64(2.5),
                who=String("w"),
                why=String("y"),
                url=String("u"),
                metric=String("m"),
                where_=String("where"),
            )
        ),
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"k","id":"id","text":"t","value":1.5,"hi":2.5,"who":"w","why":"y","url":"u","metric":"m","where":"where"}',
    )
    print("test_serialize_layout: ok")


def test_serialize_timestamps() raises:
    assert_equal(
        serialize_event(
            Event(parse_instant("2026-11-17T02:53:53.9943206+00:00"), "k")
        ),
        '{"at":"2026-11-17T02:53:53.9943206+00:00","kind":"k"}',
    )
    assert_equal(
        serialize_event(Event(parse_instant("2026-11-15T09:49:01Z"), "k")),
        '{"at":"2026-11-15T09:49:01+00:00","kind":"k"}',
    )
    assert_equal(
        serialize_event(
            Event(parse_instant("2026-11-15T09:49:01.5+05:30"), "k")
        ),
        '{"at":"2026-11-15T09:49:01.5+05:30","kind":"k"}',
    )
    print("test_serialize_timestamps: ok")


def test_parse_fields() raises:
    var e = parse_event(
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"idea","id":"i1","text":"vinci"}'
    )
    assert_equal(e.kind, "idea")
    assert_equal(e.id.value(), "i1")
    assert_equal(e.text.value(), "vinci")
    assert_true(e.value is None)
    assert_true(e.refs is None)

    # Keys match whatever their case, as the C# reader does.
    var upper = parse_event(
        '{"AT":"2026-11-15T09:49:01-06:00","Kind":"spent","ID":"i1","Value":2.5}'
    )
    assert_equal(upper.kind, "spent")
    assert_equal(upper.id.value(), "i1")
    assert_equal(upper.value.value(), 2.5)

    # Nulls are the same as absent fields, and unknown fields of any shape are skipped.
    var nulls = parse_event(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","id":null,"value":null,'
        + '"extra":{"a":[1,"}",{"b":null}],"c":"]"},"more":true,"n":-1.5e3}'
    )
    assert_true(nulls.id is None)
    assert_true(nulls.value is None)

    # Escapes: ', a surrogate pair, and the short forms.
    var escaped = parse_event(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","text":"\\u0027\\ud83d\\ude00'
        ' a\\/b\\tc\\"q\\\\"}'
    )
    assert_equal(escaped.text.value(), "'😀 a/b\tc\"q\\")

    var lists = parse_event(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"note","refs":["i1","s2"],"tags":[]}'
    )
    assert_equal(len(lists.refs.value()), 2)
    assert_equal(lists.refs.value()[1], "s2")
    assert_equal(len(lists.tags.value()), 0)

    var exp = parse_event(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"log","value":1E-07,"hi":-0.25}'
    )
    assert_equal(exp.value.value(), 1e-7)
    assert_equal(exp.hi.value(), -0.25)

    var zero = parse_event(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"log","value":0}'
    )
    assert_equal(zero.value.value(), 0.0)
    print("test_parse_fields: ok")


def test_parse_refusals() raises:
    assert_parse_refused('{"kind":"x"}')
    assert_parse_refused('{"at":null,"kind":"x"}')
    assert_parse_refused('{"at":"2026-11-15T09:49:01-06:00","kind":"x"} junk')
    assert_parse_refused(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","value":"5"}'
    )
    assert_parse_refused(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","text":"\\ud83d"}'
    )
    assert_parse_refused(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","text":"\\q"}'
    )
    assert_parse_refused('{"at":"2026-11-15T09:49:01-06:00","kind":"x",}')
    assert_parse_refused(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","value":1.}'
    )
    assert_parse_refused(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"x","text":"tab	raw"}'
    )
    print("test_parse_refusals: ok")


def test_parse_serialize_round_trip() raises:
    # A line C# wrote, with every escape kind, comes back byte for byte.
    var lines = List[String]()
    lines.append(
        '{"at":"2026-11-15T10:25:31.3748456-06:00","kind":"idea","id":"i1","text":"quo\\u0022te'
        ' \\u2265 \\u003Ctag\\u003E it\\u0027s \\uD83D\\uDE00 \\n\\t\\\\"}'
    )
    lines.append(
        '{"at":"2026-11-15T09:49:01-06:00","kind":"spent","id":"i1","value":1E-07}'
    )
    lines.append(
        '{"at":"2026-11-15T09:49:01.5+05:30","kind":"stage","id":"i1","text":"Build","value":0.25}'
    )
    for line in lines:
        assert_equal(serialize_event(parse_event(line)), line)
    print("test_parse_serialize_round_trip: ok")


def test_append_and_read() raises:
    var dir = "build/test-journal/nested"
    makedirs(dir, exist_ok=True)
    var path = dir + "/append.jsonl"
    if Path(path).exists():
        remove(path)
    var journal = Journal(path)
    var at = parse_instant("2026-11-15T09:49:01-06:00")
    journal.append(
        Event(at.copy(), "idea", id=String("i1"), text=String("alpha"))
    )
    journal.append(
        Event(at.copy(), "spent", id=String("i1"), value=Float64(5.5))
    )
    var events = journal.read()
    assert_equal(len(events), 2)
    assert_equal(events[0].text.value(), "alpha")
    assert_equal(events[1].value.value(), 5.5)
    var text = Path(path).read_text()
    assert_equal(
        text.byte_length(),
        2 * 1
        + len(serialize_event(events[0]).as_bytes())
        + len(serialize_event(events[1]).as_bytes()),
    )
    print("test_append_and_read: ok")


def round_trip(path: String) raises:
    """Parse every line of a journal and serialize it again. Lines C# wrote should
    come back byte for byte. Prints each one that does not, with its line number.
    """
    var text = Path(path).read_text()
    var lines = 0
    var same = 0
    var number = 0
    for raw in text.split("\n"):
        number += 1
        var line = String(raw)
        if line.byte_length() == 0:
            continue
        lines += 1
        var again = serialize_event(parse_event(line))
        if again == line:
            same += 1
        else:
            print("differs at line", number)
            print("  C#:    ", line)
            print("  Mojo:  ", again)
    print("round trip:", same, "of", lines, "lines are byte for byte")


def main() raises:
    test_json_string_escapes()
    test_json_double()
    test_serialize_layout()
    test_serialize_timestamps()
    test_parse_fields()
    test_parse_refusals()
    test_parse_serialize_round_trip()
    test_append_and_read()
    var args = argv()
    if len(args) > 1:
        round_trip(String(args[1]))
    print("test_journal: ok")
