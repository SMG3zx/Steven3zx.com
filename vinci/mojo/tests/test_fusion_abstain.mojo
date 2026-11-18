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
    parse_weights,
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
    var semantic = weights_for("semantic")
    assert_equal(semantic.lexical, 0.25)
    assert_equal(semantic.vector, 1.0)
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


def near(a: Float64, b: Float64) -> Bool:
    var d = a - b
    if d < 0:
        d = -d
    return d < 1e-12


def test_weighted_fusion_lets_the_vector_list_lead() raises:
    # Lexical ranks A, B; vector ranks C, D. Weights lexical 0.25, vector 1.0, k 60:
    # C (vector rank 1) = 1/61 = 0.0163934    D (vector rank 2) = 1/62 = 0.0161290
    # A (lexical rank 1) = 0.25/61 = 0.0040984  B (lexical rank 2) = 0.25/62 = 0.0040323
    # So the order is C, D, A, B. Equal weights 0.4, 0.4 tie A with C (both 0.4/61), and
    # A is seen first, so that order is A, C, B, D.
    var lexical = List[Candidate]()
    lexical.append(cand(1, "A", "alpha"))
    lexical.append(cand(2, "B", "beta"))
    var vector = List[Candidate]()
    vector.append(cand(3, "C", "gamma"))
    vector.append(cand(4, "D", "delta"))
    var weighted = fuse(lexical, vector, Weights(0.25, 1.0, 0.0))
    assert_equal(len(weighted), 4)
    assert_equal(weighted[0].id, "C")
    assert_equal(weighted[1].id, "D")
    assert_equal(weighted[2].id, "A")
    assert_equal(weighted[3].id, "B")
    assert_true(near(weighted[0].fused, 1.0 / 61.0))
    assert_true(near(weighted[2].fused, 0.25 / 61.0))
    assert_true(weighted[2].in_lexical and not weighted[2].in_vector)
    var equal = fuse(lexical, vector, Weights(0.4, 0.4, 0.2))
    assert_equal(equal[0].id, "A")
    assert_equal(equal[1].id, "C")


def test_weighted_fusion_sums_a_candidate_in_both_lists() raises:
    # C is lexical rank 3 and vector rank 1, as in the stable-ties test. With lexical 0.5
    # and vector 1.0: C = 0.5/63 + 1.0/61 = 0.0079365 + 0.0163934.
    var lexical = List[Candidate]()
    lexical.append(cand(1, "A", "alpha"))
    lexical.append(cand(2, "B", "beta"))
    lexical.append(cand(3, "C", "gamma"))
    var vector = List[Candidate]()
    vector.append(cand(3, "C", "gamma"))
    var fused = fuse(lexical, vector, Weights(0.5, 1.0, 0.0))
    assert_equal(fused[0].id, "C")
    assert_true(near(fused[0].fused, 0.5 / 63.0 + 1.0 / 61.0))
    assert_true(fused[0].in_lexical and fused[0].in_vector)


def test_rrf_k_sets_every_contribution() raises:
    # With k = 20 the first vector rank is worth 1/21, not 1/61. A zero lexical weight
    # adds nothing, so only the vector list counts.
    var vector = List[Candidate]()
    vector.append(cand(1, "C", "gamma"))
    var at_20 = fuse(List[Candidate](), vector, Weights(0.0, 1.0, 0.0, 20))
    assert_true(near(at_20[0].fused, 1.0 / 21.0))
    var at_60 = fuse(List[Candidate](), vector, Weights(0.0, 1.0, 0.0))
    assert_true(near(at_60[0].fused, 1.0 / 61.0))
    assert_equal(Weights(0.4, 0.4, 0.2).rrf_k, 60)


def test_parse_weights_reads_the_ratio_and_k() raises:
    var w = parse_weights("lexical=0.25,vector=1")
    assert_true(near(w.lexical, 0.25))
    assert_true(near(w.vector, 1.0))
    assert_equal(w.rrf_k, 60)
    var k = parse_weights("vector=1,k=20,lexical=0.5")
    assert_true(near(k.lexical, 0.5))
    assert_equal(k.rrf_k, 20)
    assert_false(k.semantic_only)
    var scoped = parse_weights("lexical=0.5,vector=1,scope=semantic")
    assert_true(scoped.semantic_only)
    assert_false(parse_weights("lexical=0.5,vector=1,scope=all").semantic_only)


def refused(text: String) raises -> Bool:
    try:
        _ = parse_weights(text)
    except:
        return True
    return False


def test_parse_weights_refuses_what_it_cannot_read() raises:
    # Each of these is a mistake an eval could make: a missing half, an unknown name, a
    # sign, a non-number, a k below one, an empty pair, or a separator other than "=".
    assert_true(refused("lexical=1"))
    assert_true(refused("vector=1"))
    assert_true(refused("lexical=0,vector=0"))
    assert_true(refused("lexical=-1,vector=1"))
    assert_true(refused("lexical=x,vector=1"))
    assert_true(refused("lexical=1,vector=1,k=0"))
    assert_true(refused("lexical=1,vector=1,k=abc"))
    assert_true(refused("lexical=1,vector=1,graph=1"))
    assert_true(refused("lexical=1;vector=1"))
    assert_true(refused("lexical=1,vector="))
    assert_true(refused("lexical=1,vector=1,scope=temporal"))
    assert_false(refused("lexical=0,vector=1"))


def main() raises:
    test_weighted_fusion_lets_the_vector_list_lead()
    print("test_weighted_fusion_lets_the_vector_list_lead: ok")
    test_weighted_fusion_sums_a_candidate_in_both_lists()
    print("test_weighted_fusion_sums_a_candidate_in_both_lists: ok")
    test_rrf_k_sets_every_contribution()
    print("test_rrf_k_sets_every_contribution: ok")
    test_parse_weights_reads_the_ratio_and_k()
    print("test_parse_weights_reads_the_ratio_and_k: ok")
    test_parse_weights_refuses_what_it_cannot_read()
    print("test_parse_weights_refuses_what_it_cannot_read: ok")
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
