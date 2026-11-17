from std.testing import assert_equal, assert_true

from cosine import Hit, Store, scan_scalar, scan_simd


struct Rng(Movable):
    """xorshift64*: small, deterministic, no stdlib dependency."""

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
        # Uniform in [-1, 1).
        var bits = Int(self.next_u64() >> 40)
        return Float32(bits) / 8388608.0 - 1.0


def random_vec(mut rng: Rng, dim: Int) -> List[Float32]:
    var v = List[Float32]()
    for _ in range(dim):
        v.append(rng.next_f32())
    return v^


def check_agree(dim: Int) raises:
    var rng = Rng(7)
    var store = Store(dim)
    for _ in range(500):
        store.insert(random_vec(rng, dim))
    var q = random_vec(rng, dim)
    var a = scan_scalar(store, q, 10)
    var b = scan_simd(store, q, 10)
    assert_equal(len(a), 10)
    assert_equal(len(b), 10)
    for i in range(10):
        assert_equal(a[i].index, b[i].index)
        assert_true(abs(a[i].score - b[i].score) < 1e-4)
    print("simd agrees with scalar at dim", dim)


def test_agree_768() raises:
    check_agree(768)


def test_agree_13() raises:
    check_agree(13)


def test_k_larger_than_n() raises:
    var rng = Rng(11)
    var store = Store(13)
    for _ in range(5):
        store.insert(random_vec(rng, 13))
    var q = random_vec(rng, 13)
    for hits in [scan_scalar(store, q, 20), scan_simd(store, q, 20)]:
        assert_equal(len(hits), 5)
        for i in range(1, 5):
            assert_true(hits[i - 1].score >= hits[i].score)
    print("k > n returns n results")


def test_zero_query() raises:
    # Defined behaviour: a zero query normalises to zero, every score is 0.0,
    # and ties keep row order, so the first k rows come back.
    var rng = Rng(3)
    var store = Store(13)
    for _ in range(20):
        store.insert(random_vec(rng, 13))
    var zero = List[Float32]()
    for _ in range(13):
        zero.append(0.0)
    for hits in [scan_scalar(store, zero, 5), scan_simd(store, zero, 5)]:
        assert_equal(len(hits), 5)
        for i in range(5):
            assert_equal(hits[i].score, 0.0)
            assert_equal(hits[i].index, i)
    print("zero query: scores 0, no crash")


def test_identical_ranks_first() raises:
    var rng = Rng(19)
    var dim = 768
    var store = Store(dim)
    var target = random_vec(rng, dim)
    for row in range(50):
        if row == 3:
            store.insert(target)
        else:
            store.insert(random_vec(rng, dim))
    for hits in [scan_scalar(store, target, 10), scan_simd(store, target, 10)]:
        assert_equal(hits[0].index, 3)
        assert_true(abs(hits[0].score - 1.0) < 1e-4)
    print("identical vector ranks first with score ~1.0")


def test_deterministic() raises:
    var a = Rng(42)
    var b = Rng(42)
    for _ in range(100):
        assert_equal(a.next_u64(), b.next_u64())
    var rng = Rng(42)
    var store = Store(768)
    for _ in range(200):
        store.insert(random_vec(rng, 768))
    var q = random_vec(rng, 768)
    var first = scan_simd(store, q, 10)
    var second = scan_simd(store, q, 10)
    for i in range(10):
        assert_equal(first[i].index, second[i].index)
        assert_equal(first[i].score, second[i].score)
    print("seeded rng and scans are deterministic")


def main() raises:
    test_agree_768()
    test_agree_13()
    test_k_larger_than_n()
    test_zero_query()
    test_identical_ranks_first()
    test_deterministic()
    print("test_cosine: ok")
