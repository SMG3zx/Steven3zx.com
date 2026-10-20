# Store concurrency: mutex vs channels vs actors vs atomics

Throwaway experiment, kept for reference. The code is in this directory; run it from `janus/janus-go`. Related: ENG-66 (the `Memory` Store).

Question: *is an actor-owned store faster than a mutex-guarded one, because it avoids mutex choke points?* Atomics were added as a fourth family. All twelve variants ran together in one session; the full side-by-side tables are in [results/results.md](results/results.md) and the raw rows in `results/results.csv`.

## Verdict

**Not on throughput. Partly on tail latency. The choke point is the single owner, not the lock.**

1. **A mutex beats an actor on speed everywhere in memory.** With 64 callers on a 50/50 mix the best sharded mutex does 66.4M ops/s, the best sharded actors 10.3M, and bare channel owners 16.9M. A naive single `sync.Mutex` does 8.4M against 3.2M for the single actor Janus uses today.
2. **One actor is a choke point exactly like one lock.** Put a slow operation (a scan of 100k records) in any single owner, whether mutex, channel or actor, and everyone waits for it. Sharding fixes it in every family; a mutex shards more cheaply than an actor.
3. **Atomics are the fastest family where they apply**, 1.4x to 2.3x the best sharded mutex and 3.8x to 26x the best sharded actors, and a scan never blocks anyone. They apply only to single-record updates of immutable values, so they cannot replace a lock around a rule that spans records or a database connection.
4. **Where actors win: predictable tails.** A FIFO mailbox is fair. Go's mutex lets lucky goroutines barge in, so unlucky callers wait several turns. The price is microsecond medians and 2 to 3x less throughput than the mutex it competes with.
5. **Where Janus actually lives, none of this matters.** With about 25 µs of work per operation (a store bound by its one database connection) every single-owner design lands within 15% of 36k ops/s, which is 1 / service time.

## What was compared

One `Store` interface (`Get`, `Update` as read-modify-write, `Scan` of everything), identical workload code, twelve variants in four families. Each family with knobs was tuned first (`results/tuning.txt`) and run at its best settings.

| family | variant | what it is |
|---|---|---|
| mutex | `coarse` | one `sync.Mutex` over a map, the naive baseline |
| mutex | `rwmutex` | one `sync.RWMutex` |
| mutex | `sharded-1024` | 1024 padded `RWMutex` shards, the best a mutex can do for a keyed store |
| channel | `single-owner` | one goroutine owns the map and takes requests on a channel (the actor model without a framework) |
| channel | `sharded-256` | 256 such goroutines |
| actor | `janus-today` | one goakt actor, `Ask`, a fresh message per call: the current persistence layout |
| actor | `single-tuned` | one actor, `Tell` plus a pooled request and reply channel, segmented mailbox, budget 256 |
| actor | `sharded-12-ask` | 12 actors, `Ask` |
| actor | `sharded-12-tuned` | 12 actors (one per logical core), `Tell` plus pooled replies, default mailbox, budget 2048 |
| atomic | `slots-cas` | a fixed array of atomic pointers to immutable records; an update copies, applies the function and swaps with CompareAndSwap, retrying on conflict. Any update function, but dense keys known up front, and an allocation per attempt |
| atomic | `syncmap-cas` | the same copy-and-swap over `sync.Map`, so keys can be arbitrary |
| atomic | `slots-fetch-add` (counter only) | an atomic add on one field of an in-place record. **Not equivalent to the others**: it ignores the update function and only does the counter increment this workload uses. It is a ceiling, never marked best in the tables |

Fairness choices: the channel and actor variants run the same `table.exec` code as the mutex variants, so an operation costs the same inside the owner. Sharded `Scan` visits one shard at a time for the mutex, channel and actor families (not a consistent snapshot); the atomic scan is a loop of atomic loads, also not a snapshot. Atomics skip the 25 µs workload: they have no critical section, so there is no owner to hold for 25 µs, and work inside a CAS loop would run in parallel and be redone on conflict, which models optimistic concurrency rather than a store bound by one database connection.

Tuning findings: the actor throughput budget (32 to 2048) barely matters; the segmented mailbox is slower for many actors and slightly better for one; more shards than cores hurts actors (12 beat 64 and 256) but helps mutexes (1024 beat 64 by 25 to 30%); pooling messages and replies (`Tell` instead of `Ask`) is worth 1.7x to 1.8x on sharded actors and about 40% on a single actor (that comparison also changes mailbox and budget).

## Results with 64 callers

Medians of 3 runs, 1.5 s each, on one machine. Bold is the best general-purpose variant in each column.

**Throughput (ops/s):**

| variant | read95 | mixed50 | write100 | hotkey | scan-blocks | slow-io-25us |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 10.37M | 8.38M | 8.01M | 8.99M | 187.1k | 36.3k |
| mutex/rwmutex | 9.16M | 7.52M | 7.80M | 8.59M | 186.3k | 39.7k |
| mutex/sharded-1024 | 121.67M | 66.36M | 50.54M | 8.60M | 88.94M | **397.8k** |
| chan/single-owner | 4.51M | 4.34M | 4.17M | 4.29M | 100.6k | 36.4k |
| chan/sharded-256 | 17.59M | 16.86M | 14.59M | 4.92M | 16.04M | 390.6k |
| actor/janus-today | 3.24M | 3.18M | 3.01M | 3.12M | 92.6k | 36.1k |
| actor/single-tuned | 4.48M | 4.44M | 4.01M | 4.31M | 96.6k | 36.3k |
| actor/sharded-12-ask | 6.00M | 6.17M | 4.99M | 3.97M | 5.51M | 346.0k |
| actor/sharded-12-tuned | 10.54M | 10.33M | 8.92M | 5.30M | 7.53M | 360.4k |
| atomic/slots-cas | **261.88M** | **126.20M** | **69.21M** | **20.05M** | **196.23M** | n/a |
| atomic/syncmap-cas | 103.11M | 42.62M | 25.94M | 8.59M | 48.97M | n/a |
| atomic/slots-fetch-add (counter only) | 336.05M | 275.84M | 274.56M | 111.21M | 299.57M | n/a |

Workloads: `read95` 95% reads / 5% updates on 10k keys; `mixed50` 50/50; `write100` all updates; `hotkey` 50/50 with 80% of operations on one key; `scan-blocks` 95/5 on 100k keys with one goroutine scanning all of them in a loop (the numbers are the foreground operations'); `slow-io-25us` 50/50 with 25 µs of work inside the owner per operation.

**Tail latency with 64 callers (p99 / p99.9) in the two workloads where it differs most:**

| variant | hotkey | scan-blocks |
|---|---:|---:|
| mutex/coarse | 229 µs / 459 µs | 6.3 ms / 10.5 ms |
| mutex/rwmutex | 262 µs / 655 µs | 7.3 ms / 14.7 ms |
| mutex/sharded-1024 | 229 µs / 655 µs | 224 ns / 229 µs |
| chan/single-owner | 41 µs / 115 µs | 1.05 ms / 1.31 ms |
| actor/janus-today | 98 µs / 229 µs | 1.05 ms / 1.31 ms |
| actor/single-tuned | 57 µs / 197 µs | 1.05 ms / 1.31 ms |
| actor/sharded-12-tuned | 33 µs / 115 µs | 115 µs / 197 µs |
| atomic/slots-cas | 5.1 µs / 131 µs | 224 ns / 448 ns |
| atomic/syncmap-cas | 262 µs / 786 µs | 640 ns / 7.2 µs |

## What the results say

**1. Speed: the lighter the owner, the faster.** Atomics (one word) beat a mutex (a record or shard) by 1.4x to 2.3x. A mutex beats a channel owner by about 2x with one owner and 3.5x to 7x when both are sharded. Sharded channels beat sharded actors by about 1.6x, while single channel and single actor owners are close at 64 callers. Each step adds a hand-off: a mutex is an atomic operation on the caller's own thread, a channel owner adds two goroutine switches, a goakt actor adds mailbox and dispatcher work on top. At one caller the gap is the same shape: 19M to 24M ops/s for a coarse mutex, 1.4M to 1.9M for a channel owner, 0.5M to 1.0M for actors.

**2. A single owner is one queue, whatever it is made of.** In `scan-blocks` every single-owner design collapses to 90k to 190k foreground ops/s while the sharded mutex does 89M and the sharded actors 7.5M. The actor is not slower than the channel or the lock here; they all wait for the scan. Hot-key traffic does the same thing without a scan: sharding cannot help, so the sharded mutex (8.6M) and the coarse mutex (9.0M) are equal and the actors are roughly half.

**3. Fairness is the actor's real advantage, and it is a tail effect.** When the owner is slow, the coarse mutex has a median in nanoseconds and a p99.9 of 10.5 ms (some callers lose the race for several scans); the single actor has a median near one scan duration and a p99.9 of 1.3 ms. On the hot key the sharded actors' p99 is 33 µs against 229 µs to 262 µs for the mutexes. At 8 callers with 25 µs of work, the mutex p99 is 3.1 ms (coarse) or 0.9 ms (rwmutex) against 262 µs to 328 µs for the actors and the channel owner, a 3x to 12x difference at identical throughput.

**4. Atomics buy their speed by narrowing the problem.**
- They need a place where one pointer or one word is the whole truth. `slots-cas` needs dense, known keys; the version that lifts that (`syncmap-cas`) falls to 0.5x to 1.0x of the sharded mutex, and a scan of 100k keys manages about 60 scans per second against 190 to 1,450 for the other variants (2,400 for `slots-cas`).
- Every update attempt allocates a record (1.0 allocs/op in `write100`, 3.0 for `syncmap-cas`), and a hot key makes writers retry: at 64 callers `slots-cas` averages about 4 to 5 attempts per update (2.4 allocs/op where 0.5 is the floor), with a p99.9 of 131 µs.
- They cannot express a rule across records, such as "one active import per project", "completing a snapshot activates it and deletes the old ones", or "deleting an account cascades". Those need mutual exclusion or a transaction, which is why the `Memory` store uses a mutex and the real store uses a database.
- A scan with atomics can see half of a multi-record change; only the atomic family has this property across the whole store.

**5. In the Janus regime, the design does not matter for throughput.** With 25 µs per operation, `coarse`, `rwmutex`, `chan/single-owner`, `actor/janus-today` and `actor/single-tuned` all land between 36.1k and 39.7k ops/s at 64 callers. Actor message overhead (1 to 2 µs) is invisible next to the work. If the store could be sharded (it cannot: the database sits behind `SetMaxOpenConns(1)`) every family gets 10x: 398k for the sharded mutex, 391k for sharded channels, 360k for sharded actors.

**6. The cheap fix inside the actor family is the messages.** `actor/janus-today` allocates about 4 more times per operation than the pooled design (4.5 against 0.5 in `mixed50`), and the pooled single actor is 40% faster. That matters for in-memory work; it is noise next to a database call.

## What this says about the hypothesis

- "Actors remove the mutex choke point" confuses the *synchronization primitive* with the *unit of ownership*. One actor serializes like one lock and adds two hand-offs per call. What removes the choke point is partitioning state across owners, and a mutex can partition state with much less overhead (1024 padded shards against 12 actors: 6.4x on `mixed50`).
- What an actor gives you is not on the chart: ordering, no lock in application code, supervision, location transparency, and a fair queue. The fairness shows up here as tail latency; it costs median latency and throughput.
- Janus's persistence actor is bound by one database connection, the regime where all single-owner designs tie. There the mailbox is an acceptable cost, not a win. `docs/goakt.md` already records that the persistence actor breaks the "never block in `Receive`" rule; with real SQL per operation it holds a dispatcher worker per call.
- For the `Memory` test double in ENG-66, a mutex-guarded map is the right tool: the cheapest design that can keep its cross-record rules.

## Caveats

- One machine: Ryzen 5 7600X, 6 cores / 12 threads, Windows 11, Go 1.27.1, goakt 4.6.3. The 64 caller goroutines and goakt's dispatcher workers share the same 12 threads. A 2-CPU host would likely look worse for actors (the dispatcher pool is `max(GOMAXPROCS, 2)`); that was not measured.
- The clock is the Windows performance counter (Go's `time.Now` ticks about every half millisecond there). Latencies are bucketed at four buckets per power of two (up to about 25% resolution) and sampled one operation in eight.
- Spread across the three repetitions is under 15% in most cells. Cells above 20%: `write100` `chan/single-owner` 1 caller (29%), `actor/janus-today` 8 callers (35%), `actor/single-tuned` 8 callers (46%), `actor/sharded-12-ask` 64 callers (23%), `mutex/coarse` 1 caller (24%), `mixed50` `atomic/syncmap-cas` 64 callers (22%), `scan-blocks` `mutex/rwmutex` 8 callers (23%). No conclusion above rests on those.
- Tuning ran on 64 callers with no per-operation work, 2 repetitions of 1 s. Rankings between neighbouring settings (for example actor budgets) are within noise; only the large effects (shard count, `Ask` against `Tell`) are trusted.
- Not tried: copy-on-write maps behind one atomic pointer, goakt routers with consistent hashing, `BatchTell`, the `Request`/reentrancy path, seqlocks. Any of these could move a family a little; none changes the structural point that one owner is one queue.
- In-memory maps are not a database. `slow-io-25us` models one with a CPU spin, not real I/O.

## Reproduce

```bash
cd janus/janus-go/experiments/store-concurrency
go run . -tune -reps 2 -dur 1s > results/tuning.txt
go run . -out results   # about 20 minutes
go run . -render -out results   # rebuild results.md from results.csv
```

Flags: `-only read95,scan-blocks`, `-variants atomic/`, `-reps`, `-dur`, `-warm`, `-gc`, `-render`. The tuned parameters at the top of `main.go` are the winners of the sweep in `results/tuning.txt`.
