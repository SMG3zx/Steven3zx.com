# Packing tests: the packed text never exceeds the budget (with the estimate, and with a
# fake tokenizer that counts words), the whole-versus-pointer split, the oversized record,
# and the stop at the first pointer that does not fit.

from std.testing import assert_equal, assert_false, assert_true

from fusion import Candidate
from handles import date_of, digest_is_cut, digest_of
from pack import Counter, estimate_counter, pack, word_count


def candidate(id: String, kind: String, text: String) -> Candidate:
    return Candidate(
        1,
        id,
        kind,
        text,
        "2026-11-01T09:00:00-06:00",
        "2026-11-01T09:00:00-06:00",
        "journal",
        True,
    )


def words_counter() -> Counter:
    return Counter(String("words"), String(""))


def repeat(unit: String, times: Int) -> String:
    var out = String()
    for _ in range(times):
        out += unit
    return out


def test_the_estimate_is_ceil_of_bytes_over_four() raises:
    var results = List[Candidate]()
    results.append(candidate("m1", "memory", "abcde"))
    results.append(candidate("m2", "memory", "fghijklmnopqrstuvwxyz"))
    results.append(candidate("m3", "memory", repeat("word ", 40)))
    for budget in range(1, 400, 3):
        var p = pack(results, budget, 2, estimate_counter())
        assert_true(p.tokens <= budget)
        assert_equal(p.tokens, (p.text.byte_length() + 3) // 4)
        assert_equal(p.method, "estimate")


def test_the_budget_is_used_up_to_the_last_line_that_fits() raises:
    var results = List[Candidate]()
    results.append(candidate("m1", "memory", "abcdefghijklmnopqrstuvwxyz"))
    var line = "[m1] 2026-11-01 memory: abcdefghijklmnopqrstuvwxyz"
    var exact = (line.byte_length() + 3) // 4
    var at_exact = pack(results, exact, 2, estimate_counter())
    assert_equal(at_exact.text, line)
    assert_equal(at_exact.whole, 1)
    var one_less = pack(results, exact - 1, 2, estimate_counter())
    assert_equal(one_less.text, "")
    assert_equal(one_less.whole, 0)
    assert_equal(one_less.omitted, 1)


def test_a_fake_tokenizer_counts_words_exactly() raises:
    var results = List[Candidate]()
    results.append(candidate("m1", "memory", "alpha beta"))
    results.append(candidate("m2", "memory", "gamma delta epsilon"))
    # "[m1] 2026-11-01 memory: alpha beta" is 5 words: [m1], the date, memory:, alpha, beta.
    var p = pack(results, 5, 2, words_counter())
    assert_equal(p.text, "[m1] 2026-11-01 memory: alpha beta")
    assert_equal(p.tokens, 5)
    assert_equal(p.method, "words")
    assert_equal(word_count("a  b\n c\t"), 3)
    # The second line is 6 words, so both whole lines need 5 + 6 = 11 words.
    var q = pack(results, 11, 2, words_counter())
    assert_equal(q.whole, 2)
    assert_equal(q.tokens, 11)
    # With 10 words the second line is not whole, and its pointer (10 words) does not fit.
    var r = pack(results, 10, 2, words_counter())
    assert_equal(r.whole, 1)
    assert_equal(r.omitted, 1)
    assert_true(r.tokens <= 10)


def test_the_best_records_are_whole_and_the_rest_are_pointers() raises:
    var long = repeat("tail ", 30) + "END"
    var results = List[Candidate]()
    results.append(candidate("m1", "memory", "first record " + long))
    results.append(candidate("m2", "memory", "second record"))
    results.append(candidate("m3", "memory", "third " + long))
    results.append(candidate("m4", "memory", "fourth"))
    var p = pack(results, 1000, 2, estimate_counter())
    assert_equal(p.whole, 2)
    assert_equal(p.pointers, 2)
    assert_equal(p.omitted, 0)
    var lines = List[String]()
    for raw in p.text.split("\n"):
        lines.append(String(raw))
    assert_equal(len(lines), 4)
    # The whole form keeps the end of the text. The pointer does not.
    assert_true(lines[0].endswith("END"))
    assert_true(lines[1] == "[m2] 2026-11-01 memory: second record")
    assert_true(lines[2].startswith("[m3] 2026-11-01 memory: third tail"))
    assert_true(lines[2].endswith("… (open m3 for more)"))
    # A short record's digest is the whole text, so its pointer has no open hint.
    assert_true(lines[3] == "[m4] 2026-11-01 memory: fourth")


def test_a_record_that_alone_exceeds_the_budget_becomes_a_pointer() raises:
    var huge = repeat("x", 1000)
    var results = List[Candidate]()
    results.append(candidate("m1", "memory", huge))
    results.append(candidate("m2", "memory", "small"))
    var p = pack(results, 60, 2, estimate_counter())
    assert_true(p.tokens <= 60)
    assert_equal(p.oversized, 1)
    # The huge record is a pointer (37 tokens); the small one still fits whole after it.
    assert_equal(p.pointers, 1)
    assert_equal(p.whole, 1)
    var lines = List[String]()
    for raw in p.text.split("\n"):
        lines.append(String(raw))
    assert_equal(len(lines), 2)
    assert_true(lines[0].find(repeat("x", 100) + "…") >= 0)
    assert_true(lines[0].endswith("(open m1 for more)"))
    assert_equal(lines[1], "[m2] 2026-11-01 memory: small")


def test_the_pack_stops_at_the_first_pointer_that_does_not_fit() raises:
    var results = List[Candidate]()
    results.append(candidate("m1", "memory", repeat("a", 300)))
    results.append(candidate("m2", "memory", repeat("b", 300)))
    results.append(candidate("m3", "memory", "c"))
    # One pointer is about 146 bytes (37 tokens). A second would need about 75 tokens.
    var p = pack(results, 40, 0, estimate_counter())
    assert_equal(p.pointers, 1)
    assert_equal(p.omitted, 2)
    assert_true(p.text.startswith("[m1] "))
    assert_true(p.text.find("[m2]") < 0)


def test_no_results_packs_to_nothing() raises:
    var empty = List[Candidate]()
    var p = pack(empty, 100, 2, estimate_counter())
    assert_equal(p.text, "")
    assert_equal(p.tokens, 0)
    assert_equal(p.omitted, 0)


def test_a_budget_under_one_is_refused() raises:
    var results = List[Candidate]()
    results.append(candidate("m1", "memory", "x"))
    var message = String()
    try:
        _ = pack(results, 0, 2, estimate_counter())
    except err:
        message = String(err)
    assert_true(message.find("--budget must be at least 1") >= 0)


def test_digest_and_date_helpers() raises:
    assert_equal(digest_of("  one\n two\tthree  "), "one two three")
    assert_false(digest_is_cut("  one\n two\tthree  "))
    assert_true(digest_is_cut(repeat("y", 101)))
    assert_false(digest_is_cut(repeat("y", 100)))
    assert_equal(date_of("2026-11-15T10:25:31.3748456-06:00"), "2026-11-15")
    assert_equal(date_of("2026"), "2026")


def main() raises:
    test_the_estimate_is_ceil_of_bytes_over_four()
    test_the_budget_is_used_up_to_the_last_line_that_fits()
    test_a_fake_tokenizer_counts_words_exactly()
    test_the_best_records_are_whole_and_the_rest_are_pointers()
    test_a_record_that_alone_exceeds_the_budget_becomes_a_pointer()
    test_the_pack_stops_at_the_first_pointer_that_does_not_fit()
    test_no_results_packs_to_nothing()
    test_a_budget_under_one_is_refused()
    test_digest_and_date_helpers()
    print("test_pack: ok")
