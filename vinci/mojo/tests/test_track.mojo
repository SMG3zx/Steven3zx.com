# The tracker's pure helpers (track.mojo): the rounding of minutes as .NET's Math.Round gives it,
# the parent folders, the UTC stamp of a request, byte splits, and the join of a folder and a name.
# The credit itself and the lines it prints are checked against the C# binary by sync_diff
# (py/vinci_diff/sync_diff.py), which needs ActivityWatch's fake server and the oracle.

from std.testing import assert_equal, assert_false, assert_true

from timeutil import Instant, parse_instant
from track import combine, inside, parent_of, round_minutes, split_byte, utc_o


def test_round_minutes_to_even_on_a_half() raises:
    # 7.5 s is 0.125 min: the scaled 12.5 rounds to the even 12. 22.5 s is 0.375: 37.5 rounds to 38.
    assert_equal(round_minutes(0.125), 0.12)
    assert_equal(round_minutes(0.375), 0.38)
    assert_equal(round_minutes(0.5 / 60.0), 0.01)
    assert_equal(round_minutes(1.02), 1.02)
    print("test_round_minutes_to_even_on_a_half: ok")


def test_parents_stop_at_the_root() raises:
    assert_equal(parent_of("/home/a/b").value(), "/home/a")
    assert_equal(parent_of("/home").value(), "/")
    assert_false(Bool(parent_of("/")))
    print("test_parents_stop_at_the_root: ok")


def test_request_stamp_has_seven_digits_and_z() raises:
    var at = parse_instant("2026-11-18T10:00:00-06:00")
    assert_equal(utc_o(at), "2026-11-18T16:00:00.0000000Z")
    var later = at.add_ticks(1234567)
    assert_equal(utc_o(later), "2026-11-18T16:00:00.1234567Z")
    print("test_request_stamp_has_seven_digits_and_z: ok")


def test_split_keeps_empty_pieces() raises:
    var parts = split_byte("a\n\nb", 10)
    assert_equal(len(parts), 3)
    assert_equal(parts[1], "")
    assert_equal(parts[2], "b")
    print("test_split_keeps_empty_pieces: ok")


def test_inside_is_a_whole_segment() raises:
    assert_true(inside("/w/vinci/src/a.mojo", "/w/vinci"))
    assert_false(inside("/w/vinci2/a.mojo", "/w/vinci"))
    assert_equal(combine("/w", "a.txt"), "/w/a.txt")
    assert_equal(combine("/", "a.txt"), "/a.txt")
    print("test_inside_is_a_whole_segment: ok")


def main() raises:
    test_round_minutes_to_even_on_a_half()
    test_parents_stop_at_the_root()
    test_request_stamp_has_seven_digits_and_z()
    test_split_keeps_empty_pieces()
    test_inside_is_a_whole_segment()
