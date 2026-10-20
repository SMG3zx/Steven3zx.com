# Mutex vs actor: head-to-head report

Throwaway experiment, kept for reference. The code is in this directory; run it from `janus/janus-go`. Related: ENG-66 (the `Memory` Store).
Question: *is an actor-owned store faster than a mutex-guarded one, because it avoids mutex choke points?*

## Verdict

**Not on throughput. Partly on tail latency. The choke point is the single owner, not the lock.**

1. **A mutex beats an actor on speed everywhere in memory.** With 64 callers on a 50/50 mix, the best sharded mutex does 62.7M ops/s, the best sharded actors 10.3M and bare channel owners 14.2M. A naive single `sync.Mutex` does 8.3M against 3.0M for the single actor Janus uses today.
2. **One actor is a choke point exactly like one mutex.** Put a slow operation (a scan of 100k records) in either and every other caller waits for it. The actor does not remove the bottleneck; it relocates it. Both get fixed by the same thing: sharding.
3. **Where actors do win: predictable tails.** A FIFO mailbox is fair. Go's mutex lets lucky goroutines barge in, so some callers wait several turns. Examples below (8x better p99 when the owner is slow, 6x on a hot key). You pay for that with microsecond medians and 2 to 3x less throughput.
4. **Where Janus actually lives, none of this matters.** When each operation costs about 25 µs (a store bound by its one database connection), every single-owner variant lands within 15% of 36k ops/s, which is 1 / service time. Actor message overhead (1 to 2 µs) disappears under real work.

## What was compared

One `Store` interface (`Get`, `Update` as read-modify-write, `Scan` of everything), identical workload code, nine variants. Each family was tuned first (`results/tuning.txt`) and run at its best settings.

| family | variant | notes |
|---|---|---|
| mutex | `coarse` | one `sync.Mutex`, the naive baseline |
| mutex | `rwmutex` | one `sync.RWMutex` |
| mutex | `sharded-1024` | 1024 padded `RWMutex` shards. The best a mutex can do for a keyed store |
| channel | `single-owner` | one goroutine owns the map, requests over a channel (the actor model without a framework) |
| channel | `sharded-256` | 256 such goroutines |
| actor | `janus-today` | one goakt actor, `Ask`, a fresh message per call. The current persistence layout |
| actor | `single-tuned` | one actor, `Tell` plus a pooled request and reply channel, segmented mailbox, budget 256 |
| actor | `sharded-12-ask` | 12 actors, `Ask` |
| actor | `sharded-12-tuned` | 12 actors (one per logical core), `Tell` plus pooled replies, default mailbox, budget 2048 |

Fairness choices: the channel and actor variants run the same `table.exec` code as the mutex variants; operations cost the same inside the owner. Sharded `Scan` visits one shard at a time for all three families (not a consistent snapshot). Tuning findings: the actor throughput budget (32 to 2048) barely matters; the segmented mailbox is slower for many actors and slightly better for one; more shards than cores hurts actors (12 beat 64 and 256) but helps mutexes (1024 beat 64 by 25 to 30%); pooling messages and replies (`Tell` instead of `Ask`) is worth 1.8x on sharded actors and 40 to 50% on a single actor (that comparison also changes mailbox and budget).

## Results (64 callers; full tables in `results/results.md`)

Medians of 3 runs, 1.5 s each. Latency percentiles come from one sampled operation in eight.

**Throughput, in memory (ops/s):**

| workload | coarse mutex | sharded mutex | channels sharded | Janus-today actor | tuned single actor | tuned sharded actors |
|---|---:|---:|---:|---:|---:|---:|
| read95 | 9.99M | **103.7M** | 16.8M | 3.17M | 4.70M | 10.3M |
| mixed50 | 8.29M | **62.7M** | 14.2M | 3.00M | 4.21M | 10.3M |
| write100 | 7.74M | **51.9M** | 14.9M | 2.95M | 4.30M | 9.45M |
| hotkey (80% on one key) | 9.87M | 9.30M | 5.07M | 3.21M | 4.30M | 5.32M |

Sharding does not help a hot key: all three families collapse onto one serialized key, and the mutex is still about 2x ahead.

**A slow operation in the owner (`scan-blocks`: one goroutine scans all 100k records in a loop; foreground ops are 95% reads):**

| variant | foreground ops/s | p50 | p99 | p99.9 |
|---|---:|---:|---:|---:|
| coarse mutex | 218k | 224 ns | 5.2 ms | 8.4 ms |
| rwmutex | 190k | 224 ns | 7.3 ms | 12.6 ms |
| channel single owner | 96k | 786 µs | 1.3 ms | 1.3 ms |
| actor, Janus-today | 91k | 786 µs | 1.0 ms | 1.3 ms |
| actor, single tuned | 93k | 786 µs | 1.3 ms | 1.6 ms |
| actor, 12 shards | 7.7M | 1.3 µs | 98 µs | 164 µs |
| channel, 256 shards | 16.3M | 640 ns | 131 µs | 459 µs |
| mutex, 1024 shards | **99.1M** | 112 ns | 224 ns | 229 µs |

Single owners tie. The actor's whole queue waits one scan (p50 786 µs, close to one scan's duration); the mutex lets most callers through between scans (p50 in nanoseconds) but starves the unlucky ones for several scans (p99.9 8 to 13 ms vs the actor's 1.3 to 1.6 ms). Sharding fixes it in every family, and the sharded mutex does it 13x faster than sharded actors.

**Fairness, hot key (64 callers):** p99 is 33 µs for sharded actors against 196 µs for the coarse mutex and 229 µs for the sharded mutex; p99.9 is 98 µs against 393 to 655 µs.

**The Janus regime, 25 µs of work inside the owner per operation (`slow-io-25us`):**

| callers | metric | coarse mutex | rwmutex | channel owner | actor Janus-today | actor single-tuned |
|---|---|---:|---:|---:|---:|---:|
| 8 | ops/s | 37.0k | 41.7k | 36.7k | 36.4k | 36.3k |
| 8 | p50 | 28.7 µs | 65.5 µs | 229 µs | 229 µs | 229 µs |
| 8 | p99 | 2.10 ms | 0.92 ms | 262 µs | 262 µs | 262 µs |
| 64 | ops/s | 36.3k | 40.6k | 35.6k | 36.1k | 36.5k |

All of them are bound by 1 / 27 µs. The mutex's median is better, but its tail is 8x worse at 8 callers (some callers keep losing the race). If you *could* shard here (you can't with `SetMaxOpenConns(1)`), every family gets 10x: 308k mutex, 292k channels, 197 to 204k actors with 8 callers.

**Allocation:** Janus-today allocates about 4 more times per op than the pooled `Tell` design (a fresh message and a fresh reply, plus the `Ask` machinery). The 0.5 per op that remains in the pooled design is shared by every variant, because the record escapes into the update function.

## What this says about the hypothesis

- "Actors remove the mutex choke point" confuses *synchronization primitive* with *unit of ownership*. A single actor serializes like a single lock, and adds two goroutine hand-offs per call. In the results, `actor/single`, `chan/single-owner` and `mutex/coarse` all show the same shape (everything serialized behind one owner), and the two message-passing designs are slower.
- What removes the choke point is partitioning state across owners. A mutex can partition state too, with far less overhead (1024 padded shards vs 12 actors: 6x on 64 callers).
- What an actor genuinely gives you is not on the chart: ordering, no lock in application code, supervision, location transparency, and a fair queue. The fairness shows up here as tail latency; it costs median latency and throughput.
- Janus's persistence actor is bound by one database connection, the regime where all these designs tie. There the mailbox is an acceptable cost, not a win. `docs/goakt.md` already notes it breaks the "never block in `Receive`" rule; with real SQL per op, that holds a dispatcher worker per call.
- For the `Memory` test double in ENG-66, a mutex-guarded map is the right tool: it is the cheapest design that has the properties tests need.

## Caveats

- One machine: Ryzen 5 7600X, 6 cores / 12 threads, Windows 11, Go 1.27.1, goakt 4.6.3. The 64 caller goroutines and goakt's dispatcher workers share the same 12 threads; a 2-CPU host would likely look worse for actors (the dispatcher pool is `max(GOMAXPROCS, 2)`); that was not measured.
- The clock is the Windows performance counter. Latencies are bucketed at four buckets per power of two (up to about 25% resolution) and sampled one op in eight.
- Spread across the three repetitions is under 15% in most cells. Noisy cells: read95 1 caller sharded actors 38%, hotkey 1 caller sharded actors 25%, write100 1 caller Janus-today 34%, scan-blocks channel sharded 22 to 31%. No conclusion above rests on those.
- Tuning ran on 64 callers with no per-op work, 2 repetitions of 1 s. Rankings between neighbouring settings (for example actor budgets) are within noise; only the large effects (shard count, `Ask` vs `Tell`) are trusted.
- Not tried: lock-free or copy-on-write reads for the mutex family, goakt routers with consistent hashing, `BatchTell`, and the `Request`/reentrancy path. Any of these could move the actor family a bit; none changes the structural point that one owner is one queue.
- In-memory maps are not a database. The `slow-io` workload is a model of one: CPU spin instead of I/O.

## Addendum: atomics

Added after the first run (results in `results-atomic/`), same machine and settings, but a separate run (so cross-table comparisons carry ordinary run-to-run noise, roughly 10%). Three designs, because atomics only fit a keyed store in particular shapes:

- **`slots-cas`**: a fixed array of atomic pointers to immutable records. A read is one atomic load; an update copies the record, applies the function and swaps with CompareAndSwap, retrying on conflict. Any update function works, but keys must be dense and known up front, and every attempt allocates a record.
- **`syncmap-cas`**: the same copy-and-swap over `sync.Map`, so keys can be arbitrary.
- **`slots-fetch-add` (counter only)**: an atomic add on one field of an in-place record. **Not equivalent to the others**: it ignores the update function and only does the counter increment this workload uses. It is a ceiling, not a design you could use for a general update.

They were not run in the 25 µs workload. They have no critical section, so there is no owner to hold for 25 µs; work inside a CAS loop would run in parallel and be redone on conflict, which models optimistic concurrency, not a store bound by one database connection.

**Throughput with 64 callers (ops/s), against the best of the earlier families:**

| workload | slots-cas | syncmap-cas | fetch-add (counter only) | sharded mutex | sharded actors |
|---|---:|---:|---:|---:|---:|
| read95 | **253.7M** | 95.9M | 336.1M | 103.7M | 10.3M |
| mixed50 | **99.2M** | 38.5M | 283.3M | 62.7M | 10.3M |
| write100 | **65.5M** | 25.5M | 289.7M | 51.9M | 9.45M |
| hotkey (80% on one key) | **18.7M** | 8.2M | 112.6M | 9.3M | 5.3M |
| scan-blocks (foreground) | **187.0M** | 44.0M | 303.2M | 99.1M | 7.7M |

**What it shows:**

1. **Where atomics apply, they are the fastest family**: `slots-cas` is 1.3x to 2.4x the best sharded mutex and 3.5x to 25x the best sharded actors (the low end is the hot key, the high end read-heavy and scan-blocked loads). Even on the hot key it beats everything else (18.7M against 9.3M to 9.9M for the mutexes), because readers never queue.
2. **They remove the scan choke point completely**: with a scanner running over 100k keys, `slots-cas` foreground p99 is 224 ns and the scanner is not slowed (about 2.2k scans/s). A scan is just loads, so nothing waits for it.
3. **The general-purpose version gives most of it back**: `syncmap-cas` is 0.4x to 0.9x of the sharded mutex and a `sync.Map` scan of 100k keys manages only about 90 scans/s.
4. **CAS retries are the cost on a hot key**: at 64 callers the hot-key run averages about 4 to 5 attempts per update (2.3 allocations per op, against 0.5 without retries), and p99.9 is 229 µs. Reads stay fast, but writers to the same key fight.
5. **Each update allocates**: `slots-cas` allocates one record per update attempt (1.0 allocs/op in write100, 3.0 for `syncmap-cas`). Garbage is the price of lock-free reads of a 64-byte value.

**Why this does not change the Janus conclusion:**

- Atomics make one word, or one pointer, change atomically. They cannot express a rule across records, such as "one active import per project", "completing a snapshot activates it and deletes the old ones", or "deleting an account cascades to its projects, jobs and builds". Those need mutual exclusion (or a transaction), which is why `Memory` uses a mutex and the real store uses a database.
- They cannot make a database connection concurrent. The real store is bound by one connection, the regime where every single-owner design ties at about 36k ops/s.
- A scan with atomics is not a snapshot: it can see half of a multi-record change. The sharded mutex and actor scans have the same property per shard, but only atomics have it across the whole store.

The ranking by how much the owner must understand holds up: atomics (one word) are fastest, then a mutex (a record or a shard), then an actor (anything, including I/O and sequences of steps). Speed falls as the amount of structure the owner can protect grows.

## Reproduce

```bash
cd janus/janus-go
go run ./experiments/mutex-vs-actor -tune -reps 2 -dur 1s > experiments/mutex-vs-actor/results/tuning.txt
go run ./experiments/mutex-vs-actor -out experiments/mutex-vs-actor/results   # about 15 minutes
go run ./experiments/mutex-vs-actor -variants atomic/ -only read95,mixed50,write100,hotkey,scan-blocks -out experiments/mutex-vs-actor/results-atomic
```

Flags: `-only read95,scan-blocks`, `-variants actor/`, `-reps`, `-dur`, `-warm`, `-gc`.
