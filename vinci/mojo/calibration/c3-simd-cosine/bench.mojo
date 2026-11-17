from std.time import perf_counter_ns

from cosine import Hit, Store, scan_scalar, scan_simd


comptime N = 100_000
comptime D = 768
comptime K = 10
comptime QUERIES = 20


struct Rng(Movable):
    var state: UInt64

    def __init__(out self, seed: UInt64):
        self.state = seed

    def next_u64(mut self) -> UInt64:
        var x = self.state
        x ^= x >> 12
        x ^= x << 25
        x ^= x >> 27
        self.state = x
        return x * 2685821657736338717

    def next_f32(mut self) -> Float32:
        var bits = Int(self.next_u64() >> 40)
        return Float32(bits) / 8388608.0 - 1.0


def median_ms(mut xs: List[Float64]) -> Float64:
    # Insertion sort: the list is only QUERIES long.
    for i in range(1, len(xs)):
        var x = xs[i]
        var j = i
        while j > 0 and xs[j - 1] > x:
            xs[j] = xs[j - 1]
            j -= 1
        xs[j] = x
    var n = len(xs)
    if n % 2 == 1:
        return xs[n // 2]
    return (xs[n // 2 - 1] + xs[n // 2]) / 2


def main() raises:
    var rng = Rng(42)
    var store = Store(D)
    var row = List[Float32]()
    for _ in range(D):
        row.append(0.0)
    for _ in range(N):
        for j in range(D):
            row[j] = rng.next_f32()
        store.insert(row)

    var scalar_ms = List[Float64]()
    var simd_ms = List[Float64]()
    var agree = True
    for _ in range(QUERIES):
        var q = List[Float32]()
        for _ in range(D):
            q.append(rng.next_f32())

        var t0 = perf_counter_ns()
        var a = scan_scalar(store, q, K)
        var t1 = perf_counter_ns()
        var b = scan_simd(store, q, K)
        var t2 = perf_counter_ns()

        scalar_ms.append(Float64(t1 - t0) / 1e6)
        simd_ms.append(Float64(t2 - t1) / 1e6)
        for i in range(K):
            if a[i].index != b[i].index:
                agree = False

    var s = median_ms(scalar_ms)
    var v = median_ms(simd_ms)
    print("N =", N, "D =", D, "k =", K, "queries =", QUERIES)
    print("scalar median ms/query:", s)
    print("simd   median ms/query:", v)
    print("speedup:", s / v)
    print("top-k indices agree on every query:", agree)
