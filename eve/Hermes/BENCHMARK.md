# Hermes performance contract

Workload: 1,000,000 entities, 60 measured ticks, profiling disabled, 32 KiB
storage target, Node v26.7.0 on the reference Windows host.

| Runtime                                     |   Median |      p95 |      p99 | ns/entity | entities/sec |
| ------------------------------------------- | -------: | -------: | -------: | --------: | -----------: |
| v0.1 baseline                               | 4.483 ms | 5.080 ms | 5.192 ms |     4.483 |       223.1M |
| v0.2 run A                                  | 4.773 ms | 5.398 ms | 5.609 ms |     4.773 |       209.5M |
| v0.2 run B                                  | 4.411 ms | 5.401 ms | 5.541 ms |     4.411 |       226.7M |
| v0.2 run C (256 KiB)                        | 2.898 ms | 3.284 ms | 3.354 ms |     2.898 |       345.1M |
| v0.2 run D (256 KiB, pre-resolved columns)  | 2.971 ms | 3.441 ms | 3.691 ms |     2.971 |       336.6M |
| v0.2 acceptance (256 KiB, 120 ticks)        | 2.994 ms | 3.694 ms | 3.891 ms |     2.994 |       334.0M |
| v0.4 unified data plane (32 KiB, 120 ticks) | 3.714 ms | 3.999 ms | 4.073 ms |     3.714 |       269.2M |

The v0.2 path meets the <=3 ns/entity target at the 256 KiB configuration in
this run. The 32 KiB default remains unchanged; chunk-size selection should be
based on repeated measurements for the deployment workload. The p95/p99 spread
still indicates runtime and garbage-collection variance, so optimization must
not hide those costs.

Run a fresh measurement with:

```bash
node Hermes.js 1000000 120
node Hermes.js --sweep
```

## Regression gates

The immutable kernel reference remains the v0.2 **4.071 ns/entity** result.
The v0.4 release gate is a median no slower than 4.50 ns/entity and p99 no
slower than 6.00 ns/entity on the reference host. Optional profiling,
checksums, recording, snapshots, and change filtering must remain outside this
kernel measurement.

Data-plane benchmark medians may regress by at most 20% from the checked-in
v0.4 reference without an explicit explanation and updated baseline. Memory
must not grow across repeated steady-state samples after request pools have
warmed.

## v0.4 data-plane reference

Command:

```bash
npm run benchmark:hermes:v04 -- <case> 100000 5
```

Each case below ran in its own Node process with explicit GC available, so
unrelated worlds and handles were not retained between workloads.

| Workload           |     Median |        p95 | ns/operation | operations/sec | 60 Hz budget |
| ------------------ | ---------: | ---------: | -----------: | -------------: | -----------: |
| Bulk commands      |   2.952 ms |   5.177 ms |       29.521 |         33.87M |        17.7% |
| Snapshot ingestion |  56.477 ms |  69.973 ms |      564.765 |          1.77M |       338.9% |
| Partial updates    |  54.009 ms |  74.590 ms |      540.090 |          1.85M |       324.1% |
| Change extraction  |   5.370 ms |   6.303 ms |       53.703 |         18.62M |        32.2% |
| Checksum           |  13.781 ms |  27.467 ms |      137.813 |          7.26M |        82.7% |
| Replay             | 108.841 ms | 117.757 ms |    1,088.412 |         0.919M |       653.0% |
| Structural commit  |  91.907 ms |  95.109 ms |      919.070 |          1.09M |       551.4% |

Snapshot, update, replay, and structural results deliberately measure a
100,000-operation burst, not an expected single-frame workload. Their budget
percentages make required batching or amortization visible rather than hiding
the cost.
