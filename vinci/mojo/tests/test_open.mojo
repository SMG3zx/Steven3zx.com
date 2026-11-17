# Open and handles tests: the line views (lines=, grep=, the cap, the errors), the handle
# ids (they never clash with a fact's cite), the digest, and what lookup finds for an id.
# Each test builds its own index in a temporary directory.

from std.python import Python, PythonObject
from std.testing import assert_equal, assert_false, assert_true

from handles import (
    Stored,
    digest_of,
    lookup,
    save_handle,
)
from index_store import Fact, IndexStore
from open_view import (
    OPEN_CAP_BYTES,
    Request,
    build_view,
    no_request,
    parse_lines_arg,
    render_json,
    render_text,
    split_lines,
)


def temp_dir() raises -> String:
    var tempfile = Python.import_module("tempfile")
    return String(tempfile.mkdtemp())


def fact(
    record_id: String,
    cite: String,
    version_hash: String,
    kind: String,
    text: String,
    status: String,
) raises -> Fact:
    var tags = List[String]()
    var refs = List[String]()
    return Fact(
        "test",
        record_id,
        version_hash,
        kind,
        text,
        cite,
        "2026-11-15T10:00:00-06:00",
        None,
        tags^,
        refs^,
        True,
        status,
        1,
    )


def numbered(count: Int, width: Int) -> String:
    """Count lines of width bytes each, numbered, with no trailing newline."""
    var out = String()
    for n in range(1, count + 1):
        if n > 1:
            out += "\n"
        var line = "line " + String(n)
        while line.byte_length() < width:
            line += "x"
        out += line
    return out


def item(text: String) -> Stored:
    return Stored("memory", "2026-11-15T10:00:00-06:00", text, "")


def expect_error(text: String, fragment: String) raises:
    """Raises unless the error message contains fragment."""
    var message = String()
    var req = no_request()
    try:
        parse_lines_arg(text, req)
    except err:
        message = String(err)
    assert_true(message.find(fragment) >= 0)


def test_split_lines_ends_without_a_blank_line() raises:
    assert_equal(len(split_lines("")), 0)
    assert_equal(len(split_lines("a\nb")), 2)
    assert_equal(len(split_lines("a\nb\n")), 2)
    assert_equal(len(split_lines("a\n\nb")), 3)


def test_range_takes_the_lines_and_clips_the_last() raises:
    var req = no_request()
    parse_lines_arg("2-9", req)
    var view = build_view("m1", item(numbered(5, 10)), req, OPEN_CAP_BYTES)
    assert_equal(view.total, 5)
    assert_equal(len(view.lines), 4)
    assert_equal(view.lines[0].n, 2)
    assert_equal(view.lines[3].n, 5)
    assert_false(view.truncated)
    assert_equal(view.more, "")


def test_range_errors_are_plain() raises:
    expect_error("5-3", "needs 1 <= A <= B")
    expect_error("0-2", "needs 1 <= A <= B")
    expect_error("-2", "needs A-B")
    expect_error("3", "needs A-B")
    expect_error("x-2", "needs whole numbers")

    var req = no_request()
    parse_lines_arg("6-7", req)
    var message = String()
    try:
        _ = build_view("m1", item(numbered(5, 10)), req, OPEN_CAP_BYTES)
    except err:
        message = String(err)
    assert_true(message.find("are past the end") >= 0)
    assert_true(message.find("has 5 lines") >= 0)


def test_grep_is_case_insensitive_with_one_line_of_context() raises:
    var text = "alpha\nBeta IDEAS here\nc\nd\ne\nf\ng\nh\nhas Ideas again\nj"
    var req = no_request()
    req.has_grep = True
    req.word = "ideas"
    var view = build_view("i1", item(text), req, OPEN_CAP_BYTES)
    var numbers = List[Int]()
    for line in view.lines:
        numbers.append(line.n)
    # Matches are lines 2 and 9. Context takes 1, 3, 8 and 10.
    assert_equal(len(numbers), 6)
    assert_equal(numbers[0], 1)
    assert_equal(numbers[1], 2)
    assert_equal(numbers[2], 3)
    assert_equal(numbers[3], 8)
    assert_equal(numbers[4], 9)
    assert_equal(numbers[5], 10)
    var shown = render_text(view)
    assert_true(shown.find("\n--\n8: ") >= 0)


def test_grep_with_no_match_says_so() raises:
    var req = no_request()
    req.has_grep = True
    req.word = "zebra"
    var view = build_view("i1", item("one\ntwo"), req, OPEN_CAP_BYTES)
    assert_equal(len(view.lines), 0)
    assert_equal(view.notice, "no line matches 'zebra'")


def test_grep_inside_a_range_keeps_only_the_range() raises:
    var req = no_request()
    req.has_grep = True
    req.word = "line"
    parse_lines_arg("4-5", req)
    var view = build_view("i1", item(numbered(10, 10)), req, OPEN_CAP_BYTES)
    # The range limits the result: context outside lines 4 and 5 is not taken.
    assert_equal(len(view.lines), 2)
    assert_equal(view.lines[0].n, 4)
    assert_equal(view.lines[1].n, 5)


def test_cap_cuts_at_a_line_and_says_how_to_read_on() raises:
    # Each line is 20 bytes and costs 21 with its newline: 190 fit in 4000 bytes.
    var view = build_view(
        "m1", item(numbered(300, 20)), no_request(), OPEN_CAP_BYTES
    )
    assert_true(view.truncated)
    assert_equal(len(view.lines), 190)
    assert_equal(view.lines[189].n, 190)
    assert_equal(view.more, "lines=191-300")
    assert_true(view.notice.find("open m1 lines=191-300") >= 0)


def test_cap_on_a_range_reads_on_inside_the_range() raises:
    var req = no_request()
    parse_lines_arg("100-300", req)
    var view = build_view("m1", item(numbered(300, 20)), req, OPEN_CAP_BYTES)
    assert_equal(view.lines[0].n, 100)
    assert_equal(view.more, "lines=290-300")


def test_one_line_over_the_cap_is_cut_inside_at_a_character() raises:
    # A two-byte character straddles byte 4001, so the cut backs off to 4000.
    var long = String()
    for _ in range(3000):
        long += "é"
    var view = build_view("d1/1", item(long), no_request(), 4001)
    assert_equal(len(view.lines), 1)
    assert_equal(view.lines[0].text.byte_length(), 4000)
    assert_true(view.notice.find("cut at 4001 bytes") >= 0)


def test_render_text_has_the_header_and_numbers() raises:
    var view = build_view(
        "i1", item("alpha\nbeta"), no_request(), OPEN_CAP_BYTES
    )
    var shown = render_text(view)
    assert_true(shown.startswith("[i1] memory 2026-11-15 (2 lines)"))
    assert_true(shown.find("\n1: alpha\n2: beta") >= 0)


def test_render_json_is_one_object() raises:
    var view = build_view("i1", item('a"b'), no_request(), OPEN_CAP_BYTES)
    var out = render_json(view)
    assert_true(out.find('"text":"a\\u0022b"') >= 0)
    assert_true(out.find('"more":null') >= 0)
    assert_true(out.find('"total_lines":1') >= 0)


def test_digest_collapses_whitespace_and_cuts_at_one_hundred() raises:
    assert_equal(digest_of("a  b\n\nc\td"), "a b c d")
    var long = String()
    for _ in range(150):
        long += "x"
    var digest = digest_of(long)
    assert_equal(digest.byte_length(), 103)
    assert_true(digest.endswith("…"))


def test_handle_ids_are_t_and_never_clash_with_a_cite() raises:
    var dir = temp_dir()
    var store = IndexStore(dir + "/index.sqlite")
    # A loaded record may already use t2 as its cite; the handles must skip it.
    _ = store.put(fact("loaded:t2", "t2", "h1", "remember", "other", "active"))
    store.conn.commit()
    var first = save_handle(store, "tool", "2026-11-17", "first text")
    var second = save_handle(store, "tool", "2026-11-17", "second text")
    var third = save_handle(store, "tool", "2026-11-17", "third text")
    assert_equal(first, "t1")
    assert_equal(second, "t3")
    assert_equal(third, "t4")
    var found = lookup(store, "t1")
    assert_equal(len(found), 1)
    assert_equal(found[0].kind, "handle")
    assert_equal(found[0].text, "first text")
    assert_equal(found[0].at, "2026-11-17")
    # The fact with cite t2 is still found under its own id, not replaced by a handle.
    var fact_found = lookup(store, "t2")
    assert_equal(len(fact_found), 1)
    assert_equal(fact_found[0].kind, "remember")


def test_lookup_finds_every_current_version_and_names_the_rest() raises:
    var dir = temp_dir()
    var store = IndexStore(dir + "/index.sqlite")
    _ = store.put(fact("idea:i1", "i1", "a", "idea", "vinci", "active"))
    _ = store.put(fact("home:i1", "i1", "b", "home", "/work/vinci", "active"))
    _ = store.put(
        fact("memory:m5", "m5", "c", "memory", "old wording", "active")
    )
    _ = store.put(
        fact("memory:m5", "m5", "d", "memory", "new wording", "active")
    )
    _ = store.put(fact("memory:m6", "m6", "e", "memory", "secret", "retracted"))
    _ = store.put(fact("memory:m7", "m7", "f", "memory", "gone", "active"))
    store.conn.commit()
    store.begin()
    _ = store.end_version("test", "memory:m7", "2026-11-16T10:00:00-06:00")
    store.commit()

    var ideas = lookup(store, "i1")
    assert_equal(len(ideas), 2)
    assert_equal(ideas[0].kind, "idea")
    assert_equal(ideas[1].kind, "home")

    var memory = lookup(store, "m5")
    assert_equal(len(memory), 1)
    assert_equal(memory[0].text, "new wording")
    assert_equal(memory[0].note, "")

    var retracted = lookup(store, "m6")
    assert_equal(len(retracted), 1)
    assert_equal(retracted[0].note, "retracted")

    var superseded = lookup(store, "m7")
    assert_equal(len(superseded), 1)
    assert_equal(superseded[0].note, "superseded")

    assert_equal(len(lookup(store, "nosuch")), 0)


def test_a_read_document_opens_as_its_title_and_body() raises:
    var dir = temp_dir()
    var store = IndexStore(dir + "/index.sqlite")
    _ = store.put(
        fact("read:d1", "d1", "t", "read", "Notes\nhttps://x.example", "active")
    )
    _ = store.put(
        fact("read:d1/1", "d1/1", "c1", "read", "alpha body", "active")
    )
    _ = store.put(
        fact("read:d1/2", "d1/2", "c2", "read", "beta body", "active")
    )
    store.conn.commit()
    var found = lookup(store, "d1")
    assert_equal(len(found), 1)
    assert_true(found[0].text.find("Notes") >= 0)
    assert_true(found[0].text.find("alpha body\n\nbeta body") >= 0)
    var chunk = lookup(store, "d1/2")
    assert_equal(len(chunk), 1)
    assert_equal(chunk[0].text, "beta body")


def test_a_handle_opens_with_the_same_views() raises:
    var dir = temp_dir()
    var store = IndexStore(dir + "/index.sqlite")
    var text = numbered(12, 10)
    var id = save_handle(store, "tool:journal", "2026-11-17", text)
    var found = lookup(store, id)
    var req = no_request()
    parse_lines_arg("11-12", req)
    var view = build_view(id, found[0], req, OPEN_CAP_BYTES)
    assert_equal(len(view.lines), 2)
    assert_true(view.lines[1].text.startswith("line 12"))
    assert_true(
        render_text(view).startswith("[t1] handle 2026-11-17 (12 lines)")
    )


def main() raises:
    test_split_lines_ends_without_a_blank_line()
    test_range_takes_the_lines_and_clips_the_last()
    test_range_errors_are_plain()
    test_grep_is_case_insensitive_with_one_line_of_context()
    test_grep_with_no_match_says_so()
    test_grep_inside_a_range_keeps_only_the_range()
    test_cap_cuts_at_a_line_and_says_how_to_read_on()
    test_cap_on_a_range_reads_on_inside_the_range()
    test_one_line_over_the_cap_is_cut_inside_at_a_character()
    test_render_text_has_the_header_and_numbers()
    test_render_json_is_one_object()
    test_digest_collapses_whitespace_and_cuts_at_one_hundred()
    test_handle_ids_are_t_and_never_clash_with_a_cite()
    test_lookup_finds_every_current_version_and_names_the_rest()
    test_a_read_document_opens_as_its_title_and_body()
    test_a_handle_opens_with_the_same_views()
    print("test_open: ok")
