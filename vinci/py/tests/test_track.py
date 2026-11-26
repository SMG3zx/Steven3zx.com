"""The tracker's pure helpers (track.py): rounding of minutes as .NET's Math.Round gives it, the
parent folders, the UTC stamp of a request, byte splits, and the join of a folder and a name. The
credit itself is checked against the C# binary by sync_diff."""

from vinci_core.timeutil import parse_instant
from vinci_core.track import combine, inside, parent_of, round_minutes, split_byte, utc_o


def test_round_minutes_to_even_on_a_half() -> None:
    # 7.5 s is 0.125 min: the scaled 12.5 rounds to the even 12. 22.5 s is 0.375: 37.5 rounds to 38.
    assert round_minutes(0.125) == 0.12
    assert round_minutes(0.375) == 0.38
    assert round_minutes(0.5 / 60.0) == 0.01
    assert round_minutes(1.02) == 1.02


def test_parents_stop_at_the_root() -> None:
    assert parent_of("/home/a/b") == "/home/a"
    assert parent_of("/home") == "/"
    assert parent_of("/") is None


def test_request_stamp_has_seven_digits_and_z() -> None:
    at = parse_instant("2026-11-18T10:00:00-06:00")
    assert utc_o(at) == "2026-11-18T16:00:00.0000000Z"
    later = at.add_ticks(1234567)
    assert utc_o(later) == "2026-11-18T16:00:00.1234567Z"


def test_split_keeps_empty_pieces() -> None:
    parts = split_byte("a\n\nb", 10)
    assert len(parts) == 3
    assert parts[1] == ""
    assert parts[2] == "b"


def test_inside_is_a_whole_segment() -> None:
    assert inside("/w/vinci/src/a.mojo", "/w/vinci")
    assert not inside("/w/vinci2/a.mojo", "/w/vinci")
    assert combine("/w", "a.txt") == "/w/a.txt"
    assert combine("/", "a.txt") == "/a.txt"
