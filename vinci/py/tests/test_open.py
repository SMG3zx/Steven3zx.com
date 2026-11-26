"""Open and handles tests: the line views (lines=, grep=, the cap, the errors), the handle ids (they
never clash with a fact's cite), the digest, and what lookup finds for an id. Each test builds its
own index in a temporary directory."""

import pytest

from vinci_core.handles import Stored, digest_of, lookup, save_handle
from vinci_core.index_store import Fact, IndexStore
from vinci_core.open_view import OPEN_CAP_BYTES, Request, build_view, parse_lines_arg, render_json, render_text, split_lines
from vinci_core.session import UserError


def fact(record_id: str, cite: str, version_hash: str, kind: str, text: str, status: str) -> Fact:
    return Fact("test", record_id, version_hash, kind, text, cite, "2026-11-15T10:00:00-06:00", None, [], [], True, status, 1)


def numbered(count: int, width: int) -> str:
    """Count lines of width bytes each, numbered, with no trailing newline."""
    out = []
    for n in range(1, count + 1):
        line = "line " + str(n)
        while len(line) < width:
            line += "x"
        out.append(line)
    return "\n".join(out)


def item(text: str) -> Stored:
    return Stored("memory", "2026-11-15T10:00:00-06:00", text, "")


def test_split_lines_ends_without_a_blank_line() -> None:
    assert len(split_lines("")) == 0
    assert len(split_lines("a\nb")) == 2
    assert len(split_lines("a\nb\n")) == 2
    assert len(split_lines("a\n\nb")) == 3


def test_range_takes_the_lines_and_clips_the_last() -> None:
    req = Request()
    parse_lines_arg("2-9", req)
    view = build_view("m1", item(numbered(5, 10)), req, OPEN_CAP_BYTES)
    assert view.total == 5
    assert len(view.lines) == 4
    assert view.lines[0].n == 2
    assert view.lines[3].n == 5
    assert not view.truncated
    assert view.more == ""


@pytest.mark.parametrize(
    "text, fragment",
    [("5-3", "needs 1 <= A <= B"), ("0-2", "needs 1 <= A <= B"), ("-2", "needs A-B"), ("3", "needs A-B"), ("x-2", "needs whole numbers")],
)
def test_range_errors_are_plain(text: str, fragment: str) -> None:
    with pytest.raises(UserError) as info:
        parse_lines_arg(text, Request())
    assert fragment in str(info.value)


def test_range_past_the_end_is_an_error() -> None:
    req = Request()
    parse_lines_arg("6-7", req)
    with pytest.raises(UserError) as info:
        build_view("m1", item(numbered(5, 10)), req, OPEN_CAP_BYTES)
    assert "are past the end" in str(info.value)
    assert "has 5 lines" in str(info.value)


def test_grep_is_case_insensitive_with_one_line_of_context() -> None:
    text = "alpha\nBeta IDEAS here\nc\nd\ne\nf\ng\nh\nhas Ideas again\nj"
    req = Request(False, 0, 0, True, "ideas")
    view = build_view("i1", item(text), req, OPEN_CAP_BYTES)
    numbers = [line.n for line in view.lines]
    # Matches are lines 2 and 9. Context takes 1, 3, 8 and 10.
    assert numbers == [1, 2, 3, 8, 9, 10]
    assert "\n--\n8: " in render_text(view)


def test_grep_with_no_match_says_so() -> None:
    req = Request(False, 0, 0, True, "zebra")
    view = build_view("i1", item("one\ntwo"), req, OPEN_CAP_BYTES)
    assert len(view.lines) == 0
    assert view.notice == "no line matches 'zebra'"


def test_grep_inside_a_range_keeps_only_the_range() -> None:
    req = Request(False, 0, 0, True, "line")
    parse_lines_arg("4-5", req)
    view = build_view("i1", item(numbered(10, 10)), req, OPEN_CAP_BYTES)
    # The range limits the result: context outside lines 4 and 5 is not taken.
    assert [line.n for line in view.lines] == [4, 5]


def test_cap_cuts_at_a_line_and_says_how_to_read_on() -> None:
    # Each line is 20 bytes and costs 21 with its newline: 190 fit in 4000 bytes.
    view = build_view("m1", item(numbered(300, 20)), Request(), OPEN_CAP_BYTES)
    assert view.truncated
    assert len(view.lines) == 190
    assert view.lines[189].n == 190
    assert view.more == "lines=191-300"
    assert "open m1 lines=191-300" in view.notice


def test_cap_on_a_range_reads_on_inside_the_range() -> None:
    req = Request()
    parse_lines_arg("100-300", req)
    view = build_view("m1", item(numbered(300, 20)), req, OPEN_CAP_BYTES)
    assert view.lines[0].n == 100
    assert view.more == "lines=290-300"


def test_one_line_over_the_cap_is_cut_inside_at_a_character() -> None:
    # A two-byte character straddles byte 4001, so the cut backs off to 4000.
    view = build_view("d1/1", item("é" * 3000), Request(), 4001)
    assert len(view.lines) == 1
    assert len(view.lines[0].text.encode("utf-8")) == 4000
    assert "cut at 4001 bytes" in view.notice


def test_render_text_has_the_header_and_numbers() -> None:
    view = build_view("i1", item("alpha\nbeta"), Request(), OPEN_CAP_BYTES)
    shown = render_text(view)
    assert shown.startswith("[i1] memory 2026-11-15 (2 lines)")
    assert "\n1: alpha\n2: beta" in shown


def test_render_json_is_one_object() -> None:
    view = build_view("i1", item('a"b'), Request(), OPEN_CAP_BYTES)
    out = render_json(view)
    assert '"text":"a\\u0022b"' in out
    assert '"more":null' in out
    assert '"total_lines":1' in out


def test_digest_collapses_whitespace_and_cuts_at_one_hundred() -> None:
    assert digest_of("a  b\n\nc\td") == "a b c d"
    digest = digest_of("x" * 150)
    assert len(digest.encode("utf-8")) == 103
    assert digest.endswith("…")


def test_handle_ids_are_t_and_never_clash_with_a_cite(tmp_path) -> None:  # type: ignore[no-untyped-def]
    store = IndexStore(str(tmp_path / "index.sqlite"))
    # A loaded record may already use t2 as its cite; the handles must skip it.
    store.put(fact("loaded:t2", "t2", "h1", "remember", "other", "active"))
    store.conn.commit()
    first = save_handle(store, "tool", "2026-11-17", "first text")
    second = save_handle(store, "tool", "2026-11-17", "second text")
    third = save_handle(store, "tool", "2026-11-17", "third text")
    assert first == "t1"
    assert second == "t3"
    assert third == "t4"
    found = lookup(store, "t1")
    assert len(found) == 1
    assert found[0].kind == "handle"
    assert found[0].text == "first text"
    assert found[0].at == "2026-11-17"
    # The fact with cite t2 is still found under its own id, not replaced by a handle.
    fact_found = lookup(store, "t2")
    assert len(fact_found) == 1
    assert fact_found[0].kind == "remember"


def test_lookup_finds_every_current_version_and_names_the_rest(tmp_path) -> None:  # type: ignore[no-untyped-def]
    store = IndexStore(str(tmp_path / "index.sqlite"))
    store.put(fact("idea:i1", "i1", "a", "idea", "vinci", "active"))
    store.put(fact("home:i1", "i1", "b", "home", "/work/vinci", "active"))
    store.put(fact("memory:m5", "m5", "c", "memory", "old wording", "active"))
    store.put(fact("memory:m5", "m5", "d", "memory", "new wording", "active"))
    store.put(fact("memory:m6", "m6", "e", "memory", "secret", "retracted"))
    store.put(fact("memory:m7", "m7", "f", "memory", "gone", "active"))
    store.conn.commit()
    store.begin()
    store.end_version("test", "memory:m7", "2026-11-16T10:00:00-06:00")
    store.commit()

    ideas = lookup(store, "i1")
    assert len(ideas) == 2
    assert ideas[0].kind == "idea"
    assert ideas[1].kind == "home"

    memory = lookup(store, "m5")
    assert len(memory) == 1
    assert memory[0].text == "new wording"
    assert memory[0].note == ""

    retracted = lookup(store, "m6")
    assert len(retracted) == 1
    assert retracted[0].note == "retracted"

    superseded = lookup(store, "m7")
    assert len(superseded) == 1
    assert superseded[0].note == "superseded"

    assert len(lookup(store, "nosuch")) == 0


def test_a_read_document_opens_as_its_title_and_body(tmp_path) -> None:  # type: ignore[no-untyped-def]
    store = IndexStore(str(tmp_path / "index.sqlite"))
    store.put(fact("read:d1", "d1", "t", "read", "Notes\nhttps://x.example", "active"))
    store.put(fact("read:d1/1", "d1/1", "c1", "read", "alpha body", "active"))
    store.put(fact("read:d1/2", "d1/2", "c2", "read", "beta body", "active"))
    store.conn.commit()
    found = lookup(store, "d1")
    assert len(found) == 1
    assert "Notes" in found[0].text
    assert "alpha body\n\nbeta body" in found[0].text
    chunk = lookup(store, "d1/2")
    assert len(chunk) == 1
    assert chunk[0].text == "beta body"


def test_a_handle_opens_with_the_same_views(tmp_path) -> None:  # type: ignore[no-untyped-def]
    store = IndexStore(str(tmp_path / "index.sqlite"))
    ident = save_handle(store, "tool:journal", "2026-11-17", numbered(12, 10))
    found = lookup(store, ident)
    req = Request()
    parse_lines_arg("11-12", req)
    view = build_view(ident, found[0], req, OPEN_CAP_BYTES)
    assert len(view.lines) == 2
    assert view.lines[1].text.startswith("line 12")
    assert render_text(view).startswith("[t1] handle 2026-11-17 (12 lines)")
