"""Packing tests: the packed text never exceeds the budget (with the estimate, and with a fake
tokenizer that counts words), the whole-versus-pointer split, the oversized record, and the stop at
the first pointer that does not fit."""

import pytest

from vinci_core.fusion import Candidate
from vinci_core.handles import date_of, digest_is_cut, digest_of
from vinci_core.pack import Counter, estimate_counter, pack, word_count
from vinci_core.session import UserError


def candidate(ident: str, kind: str, text: str) -> Candidate:
    return Candidate(1, ident, kind, text, "2026-11-01T09:00:00-06:00", "2026-11-01T09:00:00-06:00", "journal", True)


def words_counter() -> Counter:
    return Counter("words", "")


def test_the_estimate_is_ceil_of_bytes_over_four() -> None:
    results = [
        candidate("m1", "memory", "abcde"),
        candidate("m2", "memory", "fghijklmnopqrstuvwxyz"),
        candidate("m3", "memory", "word " * 40),
    ]
    for budget in range(1, 400, 3):
        p = pack(results, budget, 2, estimate_counter())
        assert p.tokens <= budget
        assert p.tokens == (len(p.text.encode("utf-8")) + 3) // 4
        assert p.method == "estimate"


def test_the_budget_is_used_up_to_the_last_line_that_fits() -> None:
    results = [candidate("m1", "memory", "abcdefghijklmnopqrstuvwxyz")]
    line = "[m1] 2026-11-01 memory: abcdefghijklmnopqrstuvwxyz"
    exact = (len(line.encode("utf-8")) + 3) // 4
    at_exact = pack(results, exact, 2, estimate_counter())
    assert at_exact.text == line
    assert at_exact.whole == 1
    one_less = pack(results, exact - 1, 2, estimate_counter())
    assert one_less.text == ""
    assert one_less.whole == 0
    assert one_less.omitted == 1


def test_a_fake_tokenizer_counts_words_exactly() -> None:
    results = [candidate("m1", "memory", "alpha beta"), candidate("m2", "memory", "gamma delta epsilon")]
    # "[m1] 2026-11-01 memory: alpha beta" is 5 words.
    p = pack(results, 5, 2, words_counter())
    assert p.text == "[m1] 2026-11-01 memory: alpha beta"
    assert p.tokens == 5
    assert p.method == "words"
    assert word_count("a  b\n c\t") == 3
    # The second line is 6 words, so both whole lines need 5 + 6 = 11 words.
    q = pack(results, 11, 2, words_counter())
    assert q.whole == 2
    assert q.tokens == 11
    # With 10 words the second line is not whole, and its pointer (10 words) does not fit.
    r = pack(results, 10, 2, words_counter())
    assert r.whole == 1
    assert r.omitted == 1
    assert r.tokens <= 10


def test_the_best_records_are_whole_and_the_rest_are_pointers() -> None:
    long = "tail " * 30 + "END"
    results = [
        candidate("m1", "memory", "first record " + long),
        candidate("m2", "memory", "second record"),
        candidate("m3", "memory", "third " + long),
        candidate("m4", "memory", "fourth"),
    ]
    p = pack(results, 1000, 2, estimate_counter())
    assert p.whole == 2
    assert p.pointers == 2
    assert p.omitted == 0
    lines = p.text.split("\n")
    assert len(lines) == 4
    # The whole form keeps the end of the text. The pointer does not.
    assert lines[0].endswith("END")
    assert lines[1] == "[m2] 2026-11-01 memory: second record"
    assert lines[2].startswith("[m3] 2026-11-01 memory: third tail")
    assert lines[2].endswith("… (open m3 for more)")
    # A short record's digest is the whole text, so its pointer has no open hint.
    assert lines[3] == "[m4] 2026-11-01 memory: fourth"


def test_a_record_that_alone_exceeds_the_budget_becomes_a_pointer() -> None:
    results = [candidate("m1", "memory", "x" * 1000), candidate("m2", "memory", "small")]
    p = pack(results, 60, 2, estimate_counter())
    assert p.tokens <= 60
    assert p.oversized == 1
    # The huge record is a pointer (37 tokens); the small one still fits whole after it.
    assert p.pointers == 1
    assert p.whole == 1
    lines = p.text.split("\n")
    assert len(lines) == 2
    assert ("x" * 100 + "…") in lines[0]
    assert lines[0].endswith("(open m1 for more)")
    assert lines[1] == "[m2] 2026-11-01 memory: small"


def test_the_pack_stops_at_the_first_pointer_that_does_not_fit() -> None:
    results = [
        candidate("m1", "memory", "a" * 300),
        candidate("m2", "memory", "b" * 300),
        candidate("m3", "memory", "c"),
    ]
    # One pointer is about 146 bytes (37 tokens). A second would need about 75 tokens.
    p = pack(results, 40, 0, estimate_counter())
    assert p.pointers == 1
    assert p.omitted == 2
    assert p.text.startswith("[m1] ")
    assert "[m2]" not in p.text


def test_no_results_packs_to_nothing() -> None:
    p = pack([], 100, 2, estimate_counter())
    assert p.text == ""
    assert p.tokens == 0
    assert p.omitted == 0


def test_a_budget_under_one_is_refused() -> None:
    with pytest.raises(UserError, match="--budget must be at least 1"):
        pack([candidate("m1", "memory", "x")], 0, 2, estimate_counter())


def test_digest_and_date_helpers() -> None:
    assert digest_of("  one\n two\tthree  ") == "one two three"
    assert not digest_is_cut("  one\n two\tthree  ")
    assert digest_is_cut("y" * 101)
    assert not digest_is_cut("y" * 100)
    assert date_of("2026-11-15T10:25:31.3748456-06:00") == "2026-11-15"
    assert date_of("2026") == "2026"
