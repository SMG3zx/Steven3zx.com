"""The top-k scan must agree with the scalar reference, and a matrix built from packed float32 bytes
(the path the index uses) must equal one built from lists."""

import random
import struct

from vinci_core.vector_scan import Matrix, scan_scalar, scan_simd
from vinci_core.vectors import floats_of


def random_vector(rng: random.Random, dim: int) -> list[float]:
    return [rng.uniform(-1.0, 1.0) for _ in range(dim)]


def test_scan_agrees_with_scalar_reference() -> None:
    rng = random.Random(20261117)
    dim = 37
    matrix = Matrix(dim)
    for _ in range(200):
        matrix.insert(random_vector(rng, dim))
    query = random_vector(rng, dim)
    k = 10
    reference = scan_scalar(matrix, query, k)
    fast = scan_simd(matrix, query, k)
    assert len(reference) == k
    assert len(fast) == k
    for i in range(k):
        assert fast[i].index == reference[i].index
        assert abs(fast[i].score - reference[i].score) < 1e-4


def test_identical_row_has_cosine_one_and_zero_row_stays_zero() -> None:
    matrix = Matrix(3)
    a = [3.0, 0.0, 4.0]
    matrix.insert(a)
    matrix.insert([0.0, 0.0, 0.0])
    hits = scan_simd(matrix, a, 2)
    assert hits[0].index == 0
    assert abs(hits[0].score - 1.0) < 1e-6
    assert hits[1].index == 1
    assert hits[1].score == 0.0


def test_matrix_from_packed_bytes_matches_list_insert() -> None:
    values = [0.5, -1.25, 2.0, 0.75]
    blob = struct.pack("<4f", *values)
    from_bytes = Matrix(4)
    from_bytes.insert(floats_of(blob))
    from_list = Matrix(4)
    from_list.insert(values)
    assert from_bytes.count == 1
    for j in range(4):
        assert abs(from_bytes.data[j] - from_list.data[j]) < 1e-6
    roundtrip = floats_of(blob)
    assert len(roundtrip) == 4
    assert roundtrip[1] == -1.25
