# Brute-force cosine top-k over one contiguous N x D Float32 buffer. Ported from
# calibration/c3-simd-cosine/cosine.mojo. Rows are normalised on insert, so the
# cosine of a row and a query is their dot product. A scan of a few thousand
# rows is milliseconds, which is why there is no ANN index (plan section 3).

from std.math import sqrt
from std.sys import simd_width_of
from std.memory import Pointer

comptime WIDTH = simd_width_of[DType.float32]()


struct Hit(Copyable, Movable):
    """One scan result: the row index and its cosine."""

    var index: Int
    var score: Float32

    def __init__(out self, index: Int, score: Float32):
        self.index = index
        self.score = score


def normalize(mut v: List[Float32]):
    """Scale v to unit length in place. A zero vector stays zero."""
    var sum: Float32 = 0
    for i in range(len(v)):
        sum += v[i] * v[i]
    if sum == 0:
        return
    var inv = 1 / sqrt(sum)
    for i in range(len(v)):
        v[i] = v[i] * inv


struct Matrix(Movable):
    """N rows of D Float32s in one row-major buffer, every row unit length."""

    var dim: Int
    var count: Int
    var data: List[Float32]

    def __init__(out self, dim: Int):
        self.dim = dim
        self.count = 0
        self.data = List[Float32]()

    def insert(mut self, vec: List[Float32]) raises:
        """Append one row, normalised. Its length must be dim."""
        if len(vec) != self.dim:
            raise Error("matrix: vector length does not match dim")
        var v = vec.copy()
        normalize(v)
        for i in range(self.dim):
            self.data.append(v[i])
        self.count += 1

    def insert_from_address(mut self, addr: Int) raises:
        """Append one row read from `dim` packed little-endian Float32s at addr.

        The caller keeps the bytes object that owns addr alive for the call. The
        copy is element by element: the pointer is a plain load, with no interop.
        """
        var src = Pointer[Float32, MutAnyOrigin](unsafe_from_address=addr)
        var v = List[Float32](length=self.dim, fill=0)
        var out = v.unsafe_ptr()
        for i in range(self.dim):
            out[unsafe_offset=i] = src[unsafe_offset=i]
        normalize(v)
        for i in range(self.dim):
            self.data.append(v[i])
        self.count += 1


struct TopK(Movable):
    """Keeps the k best hits sorted by descending score.

    Ties keep the earlier offer first, so lower row indices win ties
    when rows are offered in order.
    """

    var k: Int
    var hits: List[Hit]

    def __init__(out self, k: Int):
        self.k = k
        self.hits = List[Hit]()

    def offer(mut self, index: Int, score: Float32):
        if self.k <= 0:
            return
        var n = len(self.hits)
        if n == self.k and score <= self.hits[n - 1].score:
            return
        var pos = n
        while pos > 0 and self.hits[pos - 1].score < score:
            pos -= 1
        self.hits.insert(pos, Hit(index, score))
        if len(self.hits) > self.k:
            _ = self.hits.pop()


def normalized_query(
    matrix: Matrix, query: List[Float32]
) raises -> List[Float32]:
    if len(query) != matrix.dim:
        raise Error("scan: query length does not match matrix dim")
    var q = query.copy()
    normalize(q)
    return q^


def scan_scalar(
    matrix: Matrix, query: List[Float32], k: Int
) raises -> List[Hit]:
    """Reference scan: one plain loop per row. The SIMD scan must agree with it.
    """
    var q = normalized_query(matrix, query)
    var top = TopK(k)
    var dim = matrix.dim
    for row in range(matrix.count):
        var base = row * dim
        var acc: Float32 = 0
        for j in range(dim):
            acc += matrix.data[base + j] * q[j]
        top.offer(row, acc)
    return top.hits.copy()


def scan_simd(matrix: Matrix, query: List[Float32], k: Int) raises -> List[Hit]:
    """Scan with SIMD dot products of WIDTH lanes. A remainder loop covers the
    last dim % WIDTH elements."""
    var q = normalized_query(matrix, query)
    var top = TopK(k)
    var dim = matrix.dim
    var qp = q.unsafe_ptr()
    var dp = matrix.data.unsafe_ptr()
    for row in range(matrix.count):
        var base = row * dim
        var acc = SIMD[DType.float32, WIDTH](0)
        var j = 0
        while j + WIDTH <= dim:
            acc += dp.unsafe_load[width=WIDTH](base + j) * qp.unsafe_load[
                width=WIDTH
            ](j)
            j += WIDTH
        var score = acc.reduce_add()
        while j < dim:
            score += dp[unsafe_offset=base + j] * qp[unsafe_offset=j]
            j += 1
        top.offer(row, score)
    return top.hits.copy()
