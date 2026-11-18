# `vinci verify` rules (src/verify.mojo) on fixture text: each error and warning kind, and the
# notes that are not failures. The command itself (exit codes, the index check) is run by hand
# on a copy of the real journal (docs/development.md).

from std.testing import assert_equal, assert_true

from timeutil import TICKS_PER_DAY, parse_instant
from verify import VerifyReport, verify_text

comptime NOW = "2026-11-20T12:00:00-06:00"
comptime GOOD_1 = (
    '{"at":"2026-11-19T10:00:00-06:00","kind":"idea","id":"i1","text":"one"}'
)
comptime GOOD_2 = '{"at":"2026-11-19T11:00:00-06:00","kind":"work","id":"i1","text":"commit","where":"abc"}'
comptime GOOD_3 = (
    '{"at":"2026-11-19T12:00:00-06:00","kind":"spent","id":"i1","value":5}'
)


def now_ticks() raises -> Int:
    return parse_instant(NOW).utc


def run(text: String) raises -> VerifyReport:
    return verify_text(text, now_ticks())


def lines(*items: String) -> String:
    var out = String()
    for item in items:
        out += item + "\n"
    return out


def test_clean_journal() raises:
    var r = run(lines(GOOD_1, GOOD_2, GOOD_3))
    assert_equal(r.lines, 3)
    assert_equal(len(r.errors), 0)
    assert_equal(len(r.warnings), 0)
    assert_equal(r.notes, 0)
    assert_equal(len(r.kinds), 3)


def test_torn_last_line_is_an_error() raises:
    var r = run(GOOD_1 + "\n" + GOOD_2)
    assert_true(
        len(r.errors) >= 1, "a file without a final newline is an error"
    )


def test_bad_lines_are_errors() raises:
    # not JSON
    assert_equal(len(run(lines(GOOD_1, "not json")).errors), 1)
    # no at
    assert_equal(
        len(run(lines('{"kind":"idea","id":"i9","text":"x"}')).errors), 1
    )
    # an at without an offset
    assert_equal(
        len(
            run(
                lines('{"at":"2026-11-19T10:00:00","kind":"idea","id":"i9"}')
            ).errors
        ),
        1,
    )
    # no kind
    assert_equal(
        len(run(lines('{"at":"2026-11-19T10:00:00-06:00"}')).errors), 1
    )
    # a blank line in the middle
    assert_equal(len(run(GOOD_1 + "\n\n" + GOOD_2 + "\n").errors), 1)


def test_repeated_minted_id_is_an_error() raises:
    var r = run(
        lines(
            GOOD_1,
            '{"at":"2026-11-19T10:30:00-06:00","kind":"idea","id":"i1","text":"again"}',
        )
    )
    assert_equal(len(r.errors), 1)


def test_repeats_that_are_legitimate() raises:
    # work and spent lines on one idea id, and a versioned home line, are not errors.
    var r = run(
        lines(
            GOOD_1,
            GOOD_2,
            '{"at":"2026-11-19T11:30:00-06:00","kind":"work","id":"i1","text":"again","where":"def"}',
            GOOD_3,
            GOOD_3,
            '{"at":"2026-11-19T11:40:00-06:00","kind":"home","id":"i1","text":"/x"}',
            '{"at":"2026-11-19T11:50:00-06:00","kind":"home","id":"i1","text":"/y"}',
        )
    )
    assert_equal(len(r.errors), 0)
    assert_equal(len(r.warnings), 0)


def test_unknown_kind_and_future_time_are_warnings() raises:
    var r = run(
        lines(
            GOOD_1,
            '{"at":"2026-11-19T10:00:00-06:00","kind":"zzz","id":"q1","text":"x"}',
            '{"at":"2026-11-25T10:00:00-06:00","kind":"idea","id":"i2","text":"later"}',
        )
    )
    assert_equal(len(r.errors), 0)
    assert_equal(len(r.warnings), 2)


def test_a_day_ahead_is_not_a_warning() raises:
    var r = run(
        lines(
            GOOD_1,
            '{"at":"2026-11-21T06:00:00-06:00","kind":"idea","id":"i2","text":"x"}',
        )
    )
    assert_equal(len(r.warnings), 0)


def test_out_of_order_is_a_note() raises:
    var r = run(lines(GOOD_2, GOOD_1))
    assert_equal(len(r.errors), 0)
    assert_equal(len(r.warnings), 0)
    assert_equal(r.notes, 1)


def main() raises:
    test_clean_journal()
    test_torn_last_line_is_an_error()
    test_bad_lines_are_errors()
    test_repeated_minted_id_is_an_error()
    test_repeats_that_are_legitimate()
    test_unknown_kind_and_future_time_are_warnings()
    test_a_day_ahead_is_not_a_warning()
    test_out_of_order_is_a_note()
    print("test_verify: ok")
