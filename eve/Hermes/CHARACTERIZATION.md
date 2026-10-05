# Hermes v0.2.1 characterization findings

## Canonical kernel

Command: `node Hermes.js 1000000 120 32768` with profiling disabled.

Latest run: median **3.954 ms / 3.954 ns per entity**, p95 4.552 ms, p99
4.665 ms, 252.9M entities/sec, 23.7% of a 60 Hz frame.

This is the immutable regression benchmark. It uses one archetype and does not
retain spawn handles after insertion.

## Why characterization was ~25 ns/entity

The first v0.2.1 characterization world was not equivalent to the canonical
kernel. It retained a JavaScript array of one million generational `BigInt`
handles while also constructing the structural benchmark world. The canonical
benchmark discards those handles. The retained handles increased heap pressure
and caused GC/memory effects during later ticks and chunk cases. The
characterization process also ran multiple one-million-entity worlds in a
single process, so old ArrayBuffers were eligible for collection but not
necessarily reclaimed before the next case.

The ~25 ns/entity movement values therefore measured **workload plus retained
allocation/GC contamination**, not the Hermes movement kernel. Profile mode also
uses high-resolution timing and is intentionally not a throughput measurement.

The harness now supports `retainHandles = false` for profile and throughput
worlds, retains handles only for structural tests, and documents the mode
boundaries in its output. For explicit collection boundaries run:

```bash
node --expose-gc Hermes.js --characterize
```

## Structural evidence

At 100,000 entities on the current host, measured migration costs were:

| Transition | ns/entity |
| ---------- | --------: |
| A → B      |     804.5 |
| B → A      |     579.6 |
| A → C      |     426.8 |
| C → A      |     683.9 |

Sequential A → B measured 745 ns/entity versus deterministic-random access at
970 ns/entity. This supports grouping migration requests by source chunk and
transition before optimizing column-major copying, but does not yet prove that
grouped migration wins; that comparison belongs in Hermes v0.3.

The canonical benchmark and characterization workload are now explicitly
separate: the former detects regressions, while the latter measures structural
feature cost.

## Hermes v0.3 structural engine

Structural requests are now resolved and classified into deterministic
source-to-destination groups before migration. `grouped` is the default strategy;
`individual` preserves the per-entity baseline and `columnar` performs bulk
column copies within each source chunk while repairing locations after
swap-removal. The characterization harness reports all three under
**MIGRATION STRATEGIES** so future changes can compare throughput and commit
cost directly.
