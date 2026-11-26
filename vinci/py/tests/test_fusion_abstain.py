"""Fusion, the route score, abstention and the corrective query, on fixtures whose numbers are
worked out by hand in the comments."""

import pytest

from vinci_core.abstain import (
    NO_EVIDENCE_REASON,
    NO_IDENTIFIER_REASON,
    NO_TERMS_REASON,
    assess_evidence,
    corrective_query,
    should_abstain,
)
from vinci_core.fusion import Candidate, Weights, content_key, fuse, parse_weights, route_score, weights_for
from vinci_core.session import UserError


def cand(rowid: int, ident: str, text: str, kind: str = "idea") -> Candidate:
    return Candidate(rowid, ident, kind, text, "2026-11-01T10:00:00+00:00", "2026-11-01T10:00:00+00:00", "journal", True)


def near(a: float, b: float) -> bool:
    return abs(a - b) < 1e-12


def test_fusion_is_reciprocal_rank_with_stable_ties() -> None:
    # Weights 0.4 (lexical) and 0.4 (vector). Lexical ranks A, B, C; vector ranks C, D.
    # A: lexical rank 1 -> 0.4/61. B: lexical rank 2 -> 0.4/62. D: vector rank 2 -> 0.4/62 (ties B;
    # B was seen first). C: lexical rank 3 + vector rank 1 -> 0.4/63 + 0.4/61.
    lexical = [cand(1, "A", "alpha"), cand(2, "B", "beta"), cand(3, "C", "gamma")]
    vector = [cand(3, "C", "gamma"), cand(4, "D", "delta")]
    fused = fuse(lexical, vector, Weights(0.4, 0.4, 0.2))
    assert [c.id for c in fused] == ["C", "A", "B", "D"]
    assert fused[0].in_lexical and fused[0].in_vector
    assert fused[2].in_lexical and not fused[2].in_vector
    assert fused[3].in_vector and not fused[3].in_lexical
    assert near(fused[0].fused, 0.4 / 63.0 + 0.4 / 61.0)


def test_route_weights_match_rank_fusion() -> None:
    semantic = weights_for("semantic")
    assert semantic.lexical == 0.25
    assert semantic.vector == 1.0
    temporal = weights_for("temporal")
    assert temporal.lexical == 0.45
    assert temporal.vector == 0.2
    assert weights_for("graph").graph == 0.55
    assert weights_for("procedural").lexical == 0.5


def test_route_score_boosts_vector_only_candidates() -> None:
    only_vector = cand(1, "V", "vector only", "idea")
    only_vector.in_vector = True
    only_vector.fused = 0.5
    lexical_only = cand(2, "L", "lexical only", "idea")
    lexical_only.in_lexical = True
    lexical_only.fused = 0.5
    now = 0
    v = route_score(only_vector, "semantic", now, 1.0, 0.0)
    lex = route_score(lexical_only, "semantic", now, 1.0, 0.0)
    assert v - lex > 0.09
    # A date boost adds on top, equally to both.
    boosted = route_score(lexical_only, "semantic", now, 1.0, 0.1)
    assert boosted - lex > 0.09


def test_content_key_collapses_case_and_spacing() -> None:
    assert content_key("Alpha  BETA\n gamma") == "alpha beta gamma"
    assert content_key("   ") == ""


def test_evidence_asks_for_a_second_try_under_a_quarter() -> None:
    # Fused scores are about 0.01, far under 0.25, so the corrective pass runs on almost every
    # question. That is what the C# does, and it is kept.
    c = cand(1, "A", "alpha")
    c.fused = 0.0065
    assert assess_evidence([c]).needs_correction
    assert assess_evidence([]).needs_correction


def test_abstains_with_no_results() -> None:
    answer = should_abstain([], "anything at all")
    assert answer.abstain
    assert answer.reason == NO_EVIDENCE_REASON


def test_abstains_when_a_distinctive_identifier_is_missing() -> None:
    # "2026" is digits, so it is distinctive, and no result contains it.
    answer = should_abstain([cand(1, "A", "the offsite budget was discussed")], "What was the offsite budget for 2026?")
    assert answer.abstain
    assert answer.reason == NO_IDENTIFIER_REASON


def test_one_shared_word_is_not_evidence_for_several_terms() -> None:
    # Five meaningful terms; the result shares only "tuesday", so it covers 1 of 5.
    answer = should_abstain([cand(1, "A", "tuesday standup notes")], "gomath library tuesday meeting notes")
    assert answer.abstain
    assert answer.reason == NO_TERMS_REASON


def test_covering_most_terms_answers() -> None:
    answer = should_abstain([cand(1, "A", "portal migration is planned for spring")], "portal migration plan")
    assert not answer.abstain
    assert answer.reason == ""


def test_a_close_vector_hit_counts_without_term_coverage() -> None:
    c = cand(1, "A", "an unrelated sentence")
    c.has_cosine = True
    c.cosine = 0.5
    assert not should_abstain([c], "gomath library tuesday meeting notes").abstain


def test_stem_match_finds_a_longer_word() -> None:
    # "correction" matches "Correct": the first five letters count for six-letter terms.
    answer = should_abstain([cand(1, "A", "Correct the spelling of the note")], "correction notes")
    assert not answer.abstain


def test_corrective_query_keeps_distinct_terms_in_order() -> None:
    assert corrective_query("What did we decide about the GoMath bank?") == "decide gomath bank"
    # The same question in lower case has nothing new to say.
    assert corrective_query("decide gomath bank") == ""
    # Only function words: nothing left, so no second try.
    assert corrective_query("what is it") == ""
    assert corrective_query("alpha beta alpha gamma") == "alpha beta gamma"


def test_weighted_fusion_lets_the_vector_list_lead() -> None:
    # Lexical ranks A, B; vector ranks C, D. Weights lexical 0.25, vector 1.0, k 60:
    # C = 1/61, D = 1/62, A = 0.25/61, B = 0.25/62. So the order is C, D, A, B. Equal weights
    # 0.4, 0.4 tie A with C (both 0.4/61), and A is seen first, so that order is A, C, B, D.
    lexical = [cand(1, "A", "alpha"), cand(2, "B", "beta")]
    vector = [cand(3, "C", "gamma"), cand(4, "D", "delta")]
    weighted = fuse(lexical, vector, Weights(0.25, 1.0, 0.0))
    assert [c.id for c in weighted] == ["C", "D", "A", "B"]
    assert near(weighted[0].fused, 1.0 / 61.0)
    assert near(weighted[2].fused, 0.25 / 61.0)
    assert weighted[2].in_lexical and not weighted[2].in_vector
    equal = fuse(lexical, vector, Weights(0.4, 0.4, 0.2))
    assert equal[0].id == "A"
    assert equal[1].id == "C"


def test_weighted_fusion_sums_a_candidate_in_both_lists() -> None:
    # C is lexical rank 3 and vector rank 1. With lexical 0.5 and vector 1.0:
    # C = 0.5/63 + 1.0/61.
    lexical = [cand(1, "A", "alpha"), cand(2, "B", "beta"), cand(3, "C", "gamma")]
    vector = [cand(3, "C", "gamma")]
    fused = fuse(lexical, vector, Weights(0.5, 1.0, 0.0))
    assert fused[0].id == "C"
    assert near(fused[0].fused, 0.5 / 63.0 + 1.0 / 61.0)
    assert fused[0].in_lexical and fused[0].in_vector


def test_rrf_k_sets_every_contribution() -> None:
    # With k = 20 the first vector rank is worth 1/21, not 1/61.
    vector = [cand(1, "C", "gamma")]
    at_20 = fuse([], vector, Weights(0.0, 1.0, 0.0, 20))
    assert near(at_20[0].fused, 1.0 / 21.0)
    at_60 = fuse([], vector, Weights(0.0, 1.0, 0.0))
    assert near(at_60[0].fused, 1.0 / 61.0)
    assert Weights(0.4, 0.4, 0.2).rrf_k == 60


def test_parse_weights_reads_the_ratio_and_k() -> None:
    w = parse_weights("lexical=0.25,vector=1")
    assert near(w.lexical, 0.25)
    assert near(w.vector, 1.0)
    assert w.rrf_k == 60
    k = parse_weights("vector=1,k=20,lexical=0.5")
    assert near(k.lexical, 0.5)
    assert k.rrf_k == 20
    assert not k.semantic_only
    assert parse_weights("lexical=0.5,vector=1,scope=semantic").semantic_only
    assert not parse_weights("lexical=0.5,vector=1,scope=all").semantic_only


@pytest.mark.parametrize(
    "text",
    [
        "lexical=1",
        "vector=1",
        "lexical=0,vector=0",
        "lexical=-1,vector=1",
        "lexical=x,vector=1",
        "lexical=1,vector=1,k=0",
        "lexical=1,vector=1,k=abc",
        "lexical=1,vector=1,graph=1",
        "lexical=1;vector=1",
        "lexical=1,vector=",
        "lexical=1,vector=1,scope=temporal",
    ],
)
def test_parse_weights_refuses_what_it_cannot_read(text: str) -> None:
    with pytest.raises(UserError):
        parse_weights(text)


def test_parse_weights_accepts_zero_lexical() -> None:
    parse_weights("lexical=0,vector=1")
