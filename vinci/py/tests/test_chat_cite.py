"""The chat's citation check: which bracketed ids count as citations, which of them the tool results
of the turn did not show, and how a document passage maps to its document."""

from vinci_core.chat_cite import (
    cited_ids,
    cited_ids_with_evidence,
    document_base,
    is_cite_id,
    is_document_cite,
    unseen_cites,
)


def test_cite_ids() -> None:
    assert is_cite_id("i1")
    assert is_cite_id("m12")
    assert is_cite_id("L142")
    assert is_cite_id("d1/2")
    assert not is_cite_id("i")
    assert not is_cite_id("99")
    assert not is_cite_id("i1x")
    assert not is_cite_id("d1/")
    assert not is_cite_id("i1/")
    assert not is_cite_id("s1:p.42")


def test_cited_ids_in_an_answer() -> None:
    found = cited_ids("The idea is [i1] and [d1/2], see [i1] again. [x] [s1:p.42] [ a ] [i99]")
    assert found == ["i1", "d1/2", "i99"]


def test_unclosed_and_nested_brackets() -> None:
    assert len(cited_ids("an open [i1 and nothing else")) == 0
    assert cited_ids("[i1 and [i2]") == ["i2"]


def test_unseen_against_this_turn() -> None:
    missing = unseen_cites(["i1", "i99"], "[i1] idea: Flying machine\n")
    assert missing == ["i99"]


def test_evidence_citations() -> None:
    # An id of another shape counts when this turn's results showed it, bracketed.
    evidence = "[answer_d8e33f5c_2:0] 2022-03-09 I went to a networking event\n"
    found = cited_ids_with_evidence(
        "You went [answer_d8e33f5c_2:0], see [i99] and [1] and [see here].",
        evidence,
    )
    assert found == ["answer_d8e33f5c_2:0", "i99"]
    assert unseen_cites(found, evidence) == ["i99"]
    # A shape the results did not show is not a citation unless it has journal shape.
    assert len(cited_ids_with_evidence("[x_1:2]", evidence)) == 0


def test_documents() -> None:
    assert document_base("d1/2") == "d1"
    assert document_base("d7") == "d7"
    assert is_document_cite("d1/2")
    assert not is_document_cite("i1")
