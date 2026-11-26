"""`vinci verify` rules (vinci_core/verify.py) on fixture text: each error and warning kind, and the
notes that are not failures. The command itself (exit codes, the index check) is run by hand on a
copy of the real journal (docs/development.md)."""

from vinci_core.timeutil import parse_instant
from vinci_core.verify import verify_text

NOW = "2026-11-20T12:00:00-06:00"
GOOD_1 = '{"at":"2026-11-19T10:00:00-06:00","kind":"idea","id":"i1","text":"one"}'
GOOD_2 = '{"at":"2026-11-19T11:00:00-06:00","kind":"work","id":"i1","text":"commit","where":"abc"}'
GOOD_3 = '{"at":"2026-11-19T12:00:00-06:00","kind":"spent","id":"i1","value":5}'


def run(text: str):  # type: ignore[no-untyped-def]
    return verify_text(text, parse_instant(NOW).utc)


def lines(*items: str) -> str:
    return "".join(item + "\n" for item in items)


def test_clean_journal() -> None:
    r = run(lines(GOOD_1, GOOD_2, GOOD_3))
    assert r.lines == 3
    assert len(r.errors) == 0
    assert len(r.warnings) == 0
    assert r.notes == 0
    assert len(r.kinds) == 3


def test_torn_last_line_is_an_error() -> None:
    r = run(GOOD_1 + "\n" + GOOD_2)
    assert len(r.errors) >= 1, "a file without a final newline is an error"


def test_bad_lines_are_errors() -> None:
    assert len(run(lines(GOOD_1, "not json")).errors) == 1
    assert len(run(lines('{"kind":"idea","id":"i9","text":"x"}')).errors) == 1
    assert len(run(lines('{"at":"2026-11-19T10:00:00","kind":"idea","id":"i9"}')).errors) == 1
    assert len(run(lines('{"at":"2026-11-19T10:00:00-06:00"}')).errors) == 1
    # a blank line in the middle
    assert len(run(GOOD_1 + "\n\n" + GOOD_2 + "\n").errors) == 1


def test_repeated_minted_id_is_an_error() -> None:
    r = run(lines(GOOD_1, '{"at":"2026-11-19T10:30:00-06:00","kind":"idea","id":"i1","text":"again"}'))
    assert len(r.errors) == 1


def test_repeats_that_are_legitimate() -> None:
    # work and spent lines on one idea id, and a versioned home line, are not errors.
    r = run(
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
    assert len(r.errors) == 0
    assert len(r.warnings) == 0


def test_unknown_kind_and_future_time_are_warnings() -> None:
    r = run(
        lines(
            GOOD_1,
            '{"at":"2026-11-19T10:00:00-06:00","kind":"zzz","id":"q1","text":"x"}',
            '{"at":"2026-11-25T10:00:00-06:00","kind":"idea","id":"i2","text":"later"}',
        )
    )
    assert len(r.errors) == 0
    assert len(r.warnings) == 2


def test_a_day_ahead_is_not_a_warning() -> None:
    r = run(lines(GOOD_1, '{"at":"2026-11-21T06:00:00-06:00","kind":"idea","id":"i2","text":"x"}'))
    assert len(r.warnings) == 0


def test_out_of_order_is_a_note() -> None:
    r = run(lines(GOOD_2, GOOD_1))
    assert len(r.errors) == 0
    assert len(r.warnings) == 0
    assert r.notes == 1
