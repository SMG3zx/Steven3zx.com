"""Pure-Python reference for the cosine top-k scan, at N = 10_000 only.

Run: uv run python calibration/c3-simd-cosine/bench_py.py
The per-query median is scaled linearly to N = 100_000.
"""

import heapq
import math
import random
import statistics
import time

N = 10_000
D = 768
K = 10
QUERIES = 20


def normalize(v: list[float]) -> list[float]:
    norm = math.sqrt(sum(x * x for x in v))
    if norm == 0.0:
        return v
    return [x / norm for x in v]


def scan(rows: list[list[float]], q: list[float], k: int) -> list[tuple[float, int]]:
    scores: list[tuple[float, int]] = []
    for idx, r in enumerate(rows):
        s = 0.0
        for j in range(D):
            s += r[j] * q[j]
        scores.append((s, idx))
    return heapq.nlargest(k, scores)


def main() -> None:
    rng = random.Random(42)
    rows = [normalize([rng.uniform(-1.0, 1.0) for _ in range(D)]) for _ in range(N)]
    times_ms: list[float] = []
    for _ in range(QUERIES):
        q = normalize([rng.uniform(-1.0, 1.0) for _ in range(D)])
        t0 = time.perf_counter_ns()
        scan(rows, q, K)
        t1 = time.perf_counter_ns()
        times_ms.append((t1 - t0) / 1e6)
    med = statistics.median(times_ms)
    print(f"python N={N} D={D} k={K}: median {med:.1f} ms/query")
    print(f"scaled linearly to N=100000: {med * 10:.1f} ms/query")


if __name__ == "__main__":
    main()
