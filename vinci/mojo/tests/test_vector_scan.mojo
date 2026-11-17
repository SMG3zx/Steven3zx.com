# The SIMD top-k scan must agree with the scalar reference, and a matrix built from
# packed float32 bytes (the path the index uses) must equal one built from lists.

from std.python import Python
from std.testing import assert_equal, assert_true

from vector_scan import Matrix, scan_scalar, scan_simd
from vectors import address_of, floats_of


def lcg(mut state: UInt64) -> Float32:
    """A deterministic value in [-1, 1) from a 64-bit linear congruence."""
    state = state * 6364136223846793005 + 1442695040888963407
    var top = Int((state >> 33) & 0xFFFF)
    return Float32(top) / 32768.0 - 1.0


def random_vector(mut state: UInt64, dim: Int) -> List[Float32]:
    var v = List[Float32]()
    for _ in range(dim):
        v.append(lcg(state))
    return v^


def test_simd_scan_agrees_with_scalar_reference() raises:
    # 37 is not a multiple of the SIMD width, so the remainder loop runs too.
    var dim = 37
    var rows = 200
    var state = UInt64(20261117)
    var matrix = Matrix(dim)
    for _ in range(rows):
        matrix.insert(random_vector(state, dim))
    var query = random_vector(state, dim)
    var k = 10
    var reference = scan_scalar(matrix, query, k)
    var fast = scan_simd(matrix, query, k)
    assert_equal(len(reference), k)
    assert_equal(len(fast), k)
    for i in range(k):
        assert_equal(fast[i].index, reference[i].index)
        var diff = fast[i].score - reference[i].score
        if diff < 0:
            diff = -diff
        assert_true(diff < 1e-4)


def test_identical_row_has_cosine_one_and_zero_row_stays_zero() raises:
    var matrix = Matrix(3)
    var a = List[Float32]()
    a.append(3.0)
    a.append(0.0)
    a.append(4.0)
    matrix.insert(a)
    var zero = List[Float32]()
    zero.append(0.0)
    zero.append(0.0)
    zero.append(0.0)
    matrix.insert(zero)
    var hits = scan_simd(matrix, a, 2)
    assert_equal(hits[0].index, 0)
    var diff = hits[0].score - 1.0
    if diff < 0:
        diff = -diff
    assert_true(diff < 1e-6)
    assert_equal(hits[1].index, 1)
    assert_equal(hits[1].score, 0.0)


def test_matrix_from_packed_bytes_matches_list_insert() raises:
    var struct_ = Python.import_module("struct")
    var values = List[Float32]()
    values.append(0.5)
    values.append(-1.25)
    values.append(2.0)
    values.append(0.75)
    var blob = struct_.pack("<4f", 0.5, -1.25, 2.0, 0.75)
    var from_bytes = Matrix(4)
    from_bytes.insert_from_address(address_of(blob))
    var from_list = Matrix(4)
    from_list.insert(values)
    assert_equal(from_bytes.count, 1)
    for j in range(4):
        var diff = from_bytes.data[j] - from_list.data[j]
        if diff < 0:
            diff = -diff
        assert_true(diff < 1e-6)
    var roundtrip = floats_of(blob)
    assert_equal(len(roundtrip), 4)
    assert_equal(roundtrip[1], -1.25)


def main() raises:
    test_simd_scan_agrees_with_scalar_reference()
    print("test_simd_scan_agrees_with_scalar_reference: ok")
    test_identical_row_has_cosine_one_and_zero_row_stays_zero()
    print("test_identical_row_has_cosine_one_and_zero_row_stays_zero: ok")
    test_matrix_from_packed_bytes_matches_list_insert()
    print("test_matrix_from_packed_bytes_matches_list_insert: ok")
