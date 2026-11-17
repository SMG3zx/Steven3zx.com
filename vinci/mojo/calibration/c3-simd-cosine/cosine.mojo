from std.math import sqrt
from std.sys import simd_width_of


comptime WIDTH = simd_width_of[DType.float32]()


struct Hit(Copyable, Movable):
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


struct Store(Movable):
    """N rows of D Float32s in one contiguous row-major buffer.

    Rows are normalised on insert, so cosine similarity is a dot product.
    """

    var dim: Int
    var count: Int
    var data: List[Float32]

    def __init__(out self, dim: Int):
        self.dim = dim
        self.count = 0
        self.data = List[Float32]()

    def insert(mut self, vec: List[Float32]) raises:
        if len(vec) != self.dim:
            raise Error("insert: vector length does not match store dim")
        var v = vec.copy()
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


def scan_scalar(store: Store, query: List[Float32], k: Int) raises -> List[Hit]:
    """Reference brute-force scan: one plain loop per row."""
    if len(query) != store.dim:
        raise Error("scan: query length does not match store dim")
    var q = query.copy()
    normalize(q)
    var top = TopK(k)
    var dim = store.dim
    for row in range(store.count):
        var base = row * dim
        var acc: Float32 = 0
        for j in range(dim):
            acc += store.data[base + j] * q[j]
        top.offer(row, acc)
    return top.hits.copy()


def scan_simd(store: Store, query: List[Float32], k: Int) raises -> List[Hit]:
    """Brute-force scan with SIMD dot products of WIDTH lanes.

    A remainder loop handles the last dim % WIDTH elements.
    """
    if len(query) != store.dim:
        raise Error("scan: query length does not match store dim")
    var q = query.copy()
    normalize(q)
    var top = TopK(k)
    var dim = store.dim
    var qp = q.unsafe_ptr()
    var dp = store.data.unsafe_ptr()
    for row in range(store.count):
        var base = row * dim
        var acc = SIMD[DType.float32, WIDTH](0)
        var j = 0
        while j + WIDTH <= dim:
            acc += dp.load[width=WIDTH](base + j) * qp.load[width=WIDTH](j)
            j += WIDTH
        var score = acc.reduce_add()
        while j < dim:
            score += dp[base + j] * qp[j]
            j += 1
        top.offer(row, score)
    return top.hits.copy()
