# Qwik Twitter runtime experiment

This is a temporary, framework-neutral Twitter-like fixture for comparing the same HTTP application under Node and Bun. It is intentionally isolated from EVE. The browser UI exercises the same feed, search, like, and post interactions used by the load test.

Run from this directory with `node run.mjs`, or from the repository root with `npm run benchmark:twitter`. The default matrix runs 3 rounds of 4 scenarios at concurrency levels 1, 32, 128, and 512. Each case has a 2-second warm-up followed by 5 seconds of measurement.

Optional knobs: `$env:BENCH_DURATION_MS=10000`, `$env:BENCH_WARMUP_MS=3000`, `$env:BENCH_ROUNDS=5`, `$env:BENCH_CONCURRENCIES='1,32,128'`, and `$env:BENCH_SCENARIOS='reads,mixed'`.

The runner reports startup time, requests/interactions per second, errors, p50/p95/p99/max latency, peak RSS and heap, and cumulative user+system CPU time. Results are written to `results/latest.json` (ignored by Git). Repeat several times on an otherwise idle machine before drawing conclusions; this compares HTTP runtimes, not Qwik browser hydration or rendering.
