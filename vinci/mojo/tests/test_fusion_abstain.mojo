# Fusion, the route score, abstention and the corrective query, on fixtures whose
# numbers are worked out by hand in the comments.

from std.testing import assert_equal, assert_false, assert_true

from abstain import (
    NO_EVIDENCE_REASON,
    NO_IDENTIFIER_REASON,
    NO_TERMS_REASON,
    assess_evidence,
    corrective_query,
    should_abstain,
)
from fusion import (
    Candidate,
    Weights,
    content_key,
    fuse,
    route_score,
    weights_for,
)


def cand(
    rowid: Int, id: String, text: String, kind: String = "idea"
) raises -> Candidate:
    return Candidate(
        rowid,
        id,
        kind,
        text,
        "2026-11-01T10:00:00+00:00",
        "2026-11-01T10:00:00+00:00",
        "journal",
        True,
    )


def test_fusion_is_reciprocal_rank_with_stable_ties() raises:
    # Weights 0.4 (lexical) and 0.4 (vector). Lexical ranks A, B, C; vector ranks C, D.
    # Each contribution is weight / (60 + rank), with rank counted from 1:
    # A: lexical rank 1                       -> 0.4/61 = 0.0065574
    # B: lexical rank 2                       -> 0.4/62 = 0.0064516
    # D: vector rank 2                        -> 0.4/62 = 0.0064516 (ties B; B was seen first)
    # C: lexical rank 3 + vector rank 1       -> 0.4/63 + 0.4/61 = 0.0129066
    var lexical = List[Candidate]()
    lexical.append(cand(1, "A", "alpha"))
    lexical.append(cand(2, "B", "beta"))
    lexical.append(cand(3, "C", "gamma"))
    var vector = List[Candidate]()
    vector.append(cand(3, "C", "gamma"))
    vector.append(cand(4, "D", "delta"))
    var fused = fuse(lexical, vector, Weights(0.4, 0.4, 0.2))
    assert_equal(len(fused), 4)
    assert_equal(fused[0].id, "C")
    assert_equal(fused[1].id, "A")
    assert_equal(fused[2].id, "B")
    assert_equal(fused[3].id, "D")
    assert_true(fused[0].in_lexical and fused[0].in_vector)
    assert_true(fused[2].in_lexical and not fused[2].in_vector)
    assert_true(fused[3].in_vector and not fused[3].in_lexical)
    var diff = fused[0].fused - (0.4 / 63.0 + 0.4 / 61.0)
    if diff < 0:
        diff = -diff
    assert_true(diff < 1e-12)


def test_route_weights_match_rank_fusion() raises:
    var temporal = weights_for("temporal")
    assert_equal(temporal.lexical, 0.45)
    assert_equal(temporal.vector, 0.2)
    var graph = weights_for("graph")
    assert_equal(graph.graph, 0.55)
    var procedural = weights_for("procedural")
    assert_equal(procedural.lexical, 0.5)


def test_route_score_boosts_vector_only_candidates() raises:
    # A vector-only fact on the semantic route gets the 0.1 method boost. Its fused
    # score is scaled by the peak (1.0 here), and it is 2 years old (a tiny decay).
    var only_vector = cand(1, "V", "vector only", "idea")
    only_vector.in_vector = True
    only_vector.fused = 0.5
    var lexical_only = cand(2, "L", "lexical only", "idea")
    lexical_only.in_lexical = True
    lexical_only.fused = 0.5
    var now = 0
    var v = route_score(only_vector, "semantic", now, 1.0, 0.0)
    var l = route_score(lexical_only, "semantic", now, 1.0, 0.0)
    assert_true(v - l > 0.09)
    # A date boost adds on top, equally to both.
    var boosted = route_score(lexical_only, "semantic", now, 1.0, 0.1)
    assert_true(boosted - l > 0.09)


def test_content_key_collapses_case_and_spacing() raises:
    assert_equal(content_key("Alpha  BETA\n gamma"), "alpha beta gamma")
    assert_equal(content_key("   "), "")


def test_evidence_asks_for_a_second_try_under_a_quarter() raises:
    # Fused scores are about 0.01, far under 0.25, so the corrective pass runs on
    # almost every question. That is what the C# does, and it is kept.
    var results = List[Candidate]()
    var c = cand(1, "A", "alpha")
    c.fused = 0.0065
    results.append(c^)
    assert_true(assess_evidence(results).needs_correction)
    var none = List[Candidate]()
    assert_true(assess_evidence(none).needs_correction)


def test_abstains_with_no_results() raises:
    var none = List[Candidate]()
    var answer = should_abstain(none, "anything at all")
    assert_true(answer.abstain)
    assert_equal(answer.reason, NO_EVIDENCE_REASON)


def test_abstains_when_a_distinctive_identifier_is_missing() raises:
    # "2026" is digits, so it is distinctive, and no result contains it.
    var results = List[Candidate]()
    results.append(cand(1, "A", "the offsite budget was discussed"))
    var answer = should_abstain(
        results, "What was the offsite budget for 2026?"
    )
    assert_true(answer.abstain)
    assert_equal(answer.reason, NO_IDENTIFIER_REASON)


def test_one_shared_word_is_not_evidence_for_several_terms() raises:
    # Five meaningful terms; the result shares only "tuesday", so it covers 1 of 5.
    var results = List[Candidate]()
    results.append(cand(1, "A", "tuesday standup notes"))
    var answer = should_abstain(results, "gomath library tuesday meeting notes")
    assert_true(answer.abstain)
    assert_equal(answer.reason, NO_TERMS_REASON)


def test_covering_most_terms_answers() raises:
    var results = List[Candidate]()
    results.append(cand(1, "A", "portal migration is planned for spring"))
    var answer = should_abstain(results, "portal migration plan")
    assert_false(answer.abstain)
    assert_equal(answer.reason, "")


def test_a_close_vector_hit_counts_without_term_coverage() raises:
    var results = List[Candidate]()
    var c = cand(1, "A", "an unrelated sentence")
    c.has_cosine = True
    c.cosine = 0.5
    results.append(c^)
    var answer = should_abstain(results, "gomath library tuesday meeting notes")
    assert_false(answer.abstain)


def test_stem_match_finds_a_longer_word() raises:
    # "correction" matches "Correct": the first five letters count for six-letter terms.
    var results = List[Candidate]()
    results.append(cand(1, "A", "Correct the spelling of the note"))
    var answer = should_abstain(results, "correction notes")
    assert_false(answer.abstain)


def test_corrective_query_keeps_distinct_terms_in_order() raises:
    assert_equal(
        corrective_query("What did we decide about the GoMath bank?"),
        "decide gomath bank",
    )
    # The same question in lower case has nothing new to say.
    assert_equal(corrective_query("decide gomath bank"), "")
    # Only function words: nothing left, so no second try.
    assert_equal(corrective_query("what is it"), "")
    assert_equal(corrective_query("alpha beta alpha gamma"), "alpha beta gamma")


def main() raises:
    test_fusion_is_reciprocal_rank_with_stable_ties()
    print("test_fusion_is_reciprocal_rank_with_stable_ties: ok")
    test_route_weights_match_rank_fusion()
    print("test_route_weights_match_rank_fusion: ok")
    test_route_score_boosts_vector_only_candidates()
    print("test_route_score_boosts_vector_only_candidates: ok")
    test_content_key_collapses_case_and_spacing()
    print("test_content_key_collapses_case_and_spacing: ok")
    test_evidence_asks_for_a_second_try_under_a_quarter()
    print("test_evidence_asks_for_a_second_try_under_a_quarter: ok")
    test_abstains_with_no_results()
    print("test_abstains_with_no_results: ok")
    test_abstains_when_a_distinctive_identifier_is_missing()
    print("test_abstains_when_a_distinctive_identifier_is_missing: ok")
    test_one_shared_word_is_not_evidence_for_several_terms()
    print("test_one_shared_word_is_not_evidence_for_several_terms: ok")
    test_covering_most_terms_answers()
    print("test_covering_most_terms_answers: ok")
    test_a_close_vector_hit_counts_without_term_coverage()
    print("test_a_close_vector_hit_counts_without_term_coverage: ok")
    test_stem_match_finds_a_longer_word()
    print("test_stem_match_finds_a_longer_word: ok")
    test_corrective_query_keeps_distinct_terms_in_order()
    print("test_corrective_query_keeps_distinct_terms_in_order: ok")
