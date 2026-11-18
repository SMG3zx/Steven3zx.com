# The chat's citation check: which bracketed ids count as citations, which of them the tool
# results of the turn did not show, and how a document passage maps to its document.

from std.testing import assert_equal, assert_false, assert_true

from chat_cite import (
    cited_ids,
    document_base,
    is_cite_id,
    is_document_cite,
    unseen_cites,
)


def test_cite_ids() raises:
    assert_true(is_cite_id("i1"))
    assert_true(is_cite_id("m12"))
    assert_true(is_cite_id("L142"))
    assert_true(is_cite_id("d1/2"))
    assert_false(is_cite_id("i"))
    assert_false(is_cite_id("99"))
    assert_false(is_cite_id("i1x"))
    assert_false(is_cite_id("d1/"))
    assert_false(is_cite_id("i1/"))
    assert_false(is_cite_id("s1:p.42"))
    print("test_cite_ids: ok")


def test_cited_ids_in_an_answer() raises:
    var found = cited_ids(
        "The idea is [i1] and [d1/2], see [i1] again. [x] [s1:p.42] [ a ] [i99]"
    )
    assert_equal(len(found), 3)
    assert_equal(found[0], "i1")
    assert_equal(found[1], "d1/2")
    assert_equal(found[2], "i99")
    print("test_cited_ids_in_an_answer: ok")


def test_unclosed_and_nested_brackets() raises:
    assert_equal(len(cited_ids("an open [i1 and nothing else")), 0)
    var found = cited_ids("[i1 and [i2]")
    assert_equal(len(found), 1)
    assert_equal(found[0], "i2")
    print("test_unclosed_and_nested_brackets: ok")


def test_unseen_against_this_turn() raises:
    var cited = List[String]()
    cited.append("i1")
    cited.append("i99")
    var missing = unseen_cites(cited, "[i1] idea: Flying machine\n")
    assert_equal(len(missing), 1)
    assert_equal(missing[0], "i99")
    print("test_unseen_against_this_turn: ok")


def test_documents() raises:
    assert_equal(document_base("d1/2"), "d1")
    assert_equal(document_base("d7"), "d7")
    assert_true(is_document_cite("d1/2"))
    assert_false(is_document_cite("i1"))
    print("test_documents: ok")


def main() raises:
    test_cite_ids()
    test_cited_ids_in_an_answer()
    test_unclosed_and_nested_brackets()
    test_unseen_against_this_turn()
    test_documents()
    print("test_chat_cite: ok")
