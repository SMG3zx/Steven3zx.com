"""Brute-force cosine top-k over one N x D matrix. Rows are normalised on insert, so the
cosine of a row and a query is their dot product. A scan of a few thousand rows is
milliseconds, which is why there is no ANN index (plan section 3).

Port of mojo/src/vector_scan.mojo. Scores are Python floats (float64) where Mojo used
float32 SIMD lanes; the ordering is the same up to ties at the last bit.
"""

from __future__ import annotations

import math
from dataclasses import dataclass


@dataclass(frozen=True)
class Hit:
    """One scan result: the row index and its cosine."""

    index: int
    score: float


def normalize(v: list[float]) -> list[float]:
    """Scale v to unit length. A zero vector stays zero."""
    total = 0.0
    for x in v:
        total += x * x
    if total == 0:
        return list(v)
    inv = 1.0 / math.sqrt(total)
    return [x * inv for x in v]


class Matrix:
    """N rows of D floats in one row-major list, every row unit length."""

    def __init__(self, dim: int) -> None:
        self.dim: int = dim
        self.count: int = 0
        self.data: list[float] = []

    def insert(self, vec: list[float]) -> None:
        """Append one row, normalised. Its length must be dim."""
        if len(vec) != self.dim:
            raise ValueError("matrix: vector length does not match dim")
        self.data.extend(normalize(vec))
        self.count += 1


class TopK:
    """Keeps the k best hits sorted by descending score.

    Ties keep the earlier offer first, so lower row indices win ties when rows are offered
    in order.
    """

    def __init__(self, k: int) -> None:
        self.k: int = k
        self.hits: list[Hit] = []

    def offer(self, index: int, score: float) -> None:
        if self.k <= 0:
            return
        n = len(self.hits)
        if n == self.k and score <= self.hits[n - 1].score:
            return
        pos = n
        while pos > 0 and self.hits[pos - 1].score < score:
            pos -= 1
        self.hits.insert(pos, Hit(index, score))
        if len(self.hits) > self.k:
            self.hits.pop()


def normalized_query(matrix: Matrix, query: list[float]) -> list[float]:
    if len(query) != matrix.dim:
        raise ValueError("scan: query length does not match matrix dim")
    return normalize(query)


def scan_scalar(matrix: Matrix, query: list[float], k: int) -> list[Hit]:
    """Reference scan: one plain loop per row."""
    q = normalized_query(matrix, query)
    top = TopK(k)
    dim = matrix.dim
    for row in range(matrix.count):
        base = row * dim
        acc = 0.0
        for j in range(dim):
            acc += matrix.data[base + j] * q[j]
        top.offer(row, acc)
    return list(top.hits)


def scan_simd(matrix: Matrix, query: list[float], k: int) -> list[Hit]:
    """The scan. (The Mojo version used SIMD lanes; this is the same arithmetic in order.)"""
    return scan_scalar(matrix, query, k)
