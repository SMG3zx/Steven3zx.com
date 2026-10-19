# Platform fact checks

Checked on 2026-10-19 for the Janus platform map (Linear ENG-44). Each item says how it was checked and how much to trust it.

## Storage prices (replaces the $0.15/GiB-month placeholder)

**Decided 2026-10-19: Janus charges $0.03 per GiB-month for storage.**

| Provider | Storage | Operations | Egress | Source |
|---|---|---|---|---|
| Cloudflare R2 Standard | $0.015 per GB-month | Class A $4.50 per million, Class B $0.36 per million | none | [Cloudflare R2 pricing](https://developers.cloudflare.com/r2/pricing) (official, via search result) |
| Cloudflare R2 Infrequent Access | $0.01 per GB-month plus $0.01/GB retrieval | Class A $9.00 per million, Class B $0.90 per million | none | same |
| Amazon S3 Standard, first 50 TB, us-east-1 | $0.023 per GB-month | billed separately | billed separately | third-party pages quoting the AWS Price List API, not AWS's own page |
| Fly.io volumes | $0.15 per GB-month | none | n/a | Fly pricing docs (from the earlier research) |

R2 Standard includes a free tier of 10 GB-months, 1 million Class A and 10 million Class B requests each month.

The old $0.15 placeholder came from Fly volumes, which are block storage. Janus stores snapshots and artifacts, which are object-shaped, so R2 and S3 are the better comparison. Storage costs roughly 6 to 10 times less than the placeholder.

## Lambda price check

Confirmed on [AWS Lambda pricing](https://aws.amazon.com/lambda/pricing/) (fetched directly): $0.0000166667 per GB-second, $0.20 per million requests, 1 million requests and 400,000 GB-seconds free each month. The page sends data transfer out to the EC2 on-demand rates and does not state them. The $0.09/GB first-tier egress figure is therefore still third-party.

## Still unverified

- **Google Cloud Run:** Google's page could not be read by the fetch tool (it returned only the title). The earlier figures stand as third-party and search-snippet figures.
- **Azure Container Apps:** the per-second CPU and memory rates were never retrieved. Only the free grant and the bandwidth page figures are known.
- **Cloudflare Workers, Fastly Compute, Fly egress:** unchanged from the earlier research comment on ENG-57.

## Headscale pre-auth key request

Not confirmed. The Headscale docs say the REST API uses a Bearer API key created with `headscale apikeys create`, and that the schema is served by each server at `/swagger`. The docs do not show the pre-auth key request body. From the CLI and server code, the request carries a user, a reusable flag, an ephemeral flag, an expiration, and a list of tags that must each start with `tag:`. Field names may differ between releases.

**To settle it:** run the Headscale version we deploy and read its `/swagger` page. Do this when the enrollment spec is written, against the pinned version.

## Fuel-to-CPU-second constant

Measured on 2026-10-19 with `TestMeasureFuelPerSecond` (`janus/janus-go/actors/wasmruntime/measure_cgo_test.go`). Each workload ran a single-threaded module through Wasmtime with the same settings as a real run (fuel metering and epoch interruption on), with a fuel budget large enough to run 0.2 to 2 seconds so the host clock could time it. Hardware: AMD Ryzen 5 7600X, one core.

| Workload | Fuel per second (median, billions) | Spread across 7 runs |
|---|---|--- |
| Memory loads and stores | 25.2 | 18.2 to 29.6 |
| Integer arithmetic | 16.6 | 13.3 to 17.1 |
| Function calls (recursive fib) | 3.4 | 3.1 to 3.6 |
| Floating point (sqrt) | 2.3 | 2.0 to 2.4 |

A separate earlier pass gave 18.4 and 28.1 for the first two, so a quiet machine reads higher than a busy one.

**What it means:** fuel is not proportional to CPU time. Fuel counts operations, and an operation costs very different amounts of time: a square root or a call takes about ten times longer per fuel than a plain load or add. Any single constant therefore overcharges memory-heavy work and undercharges sqrt-heavy work. The geometric mean of the four workloads is about 7.6 billion fuel per second.

**Caveats:**

- These are synthetic loops. Real artifacts compiled from Go, Rust, or JavaScript mix operation types, so they will land inside this range. A calibration against real compiled artifacts would narrow it.
- The numbers belong to this machine. Re-measure on the hardware that serves runs, because the constant scales with it.
- Fuel stays the only deterministic meter, as the metering research found. The choice is whether to bill CPU on fuel through one constant, or on host-measured CPU time.

## How other platforms bill CPU

Searched 2026-10-19. Figures come from search results and vendor docs; confirm on each vendor's pricing page before quoting them.

- **No commercial platform found that bills WebAssembly by instruction count or fuel.** Fuel and gas metering shows up as a sandbox limit in runtimes (Wasmtime fuel, WasmEdge `--gas-limit`) and in blockchains, not in serverless invoices.
- **Edge and serverless platforms bill measured active CPU time**, and leave out time spent waiting on I/O:
  - [Vercel Active CPU](https://vercel.com/docs/fluid-compute/pricing): CPU time in milliseconds while code executes. Memory is a separate meter billed for the instance's whole lifetime in GB-hours. Both are the same split as the ENG-57 layers.
  - [Deno Deploy pricing](https://deno.com/pricing): CPU time as a pooled monthly allowance, excluding async I/O.
  - Wasmer Edge: active CPU hours and provisioned memory GB-hours as separate meters (from a [third-party profile](https://apis.io/finops/wasmer/wasmer-finops/), unconfirmed).
  - Cloudflare Workers and Fastly Compute: per CPU-millisecond and vCPU-second, from the earlier research.
- **Blockchains pair gas with benchmarks.** Polkadot's Substrate derives each operation's "weight" by benchmarking on a published [reference machine](https://docs.substrate.io/test/benchmark). A 2024 polkadot-sdk change found the reference numbers had drifted 10 to 30 percent from the machine used to generate weights, so the calibration needs maintenance.

**What it means for Janus:** the usual way to bill CPU is host-measured active CPU time. Fuel-based billing would be unusual, and as measured it is off by up to ten times between workload types. Benchmark calibration has a precedent, but it is done on the platform against a reference machine, not per application.

## Active CPU time measurement (prototype)

Built 2026-10-19 in `janus/janus-go/actors/wasmruntime` (`cputime*.go`). Each run is pinned to one OS thread and the thread's CPU clock is read before and after the module's `_start` call. Linux reads `CLOCK_THREAD_CPUTIME_ID` (nanosecond resolution). Windows reads `GetThreadTimes`, which advances by scheduler tick (about 15.6 ms). Other platforms report no CPU time.

Checks that passed on Windows with clang:
- A thread that spins for 300 ms reports at least 150 ms of CPU time and never more than its wall time.
- A thread that sleeps for 300 ms reports under 100 ms, so waiting is not counted.
- CPU-bound Wasm workloads (0.2 to 2 seconds each) report a median CPU time of 0.93 to 0.99 of wall time.

**Periodic reading (added 2026-10-19):** `measureCallSampled` runs a sampler goroutine that reads the pinned thread's CPU clock every interval and hands the running total to a callback, so a long-running instance can feed the 60-second usage report. The thread's clock is reached from another thread by id: on Linux through the per-thread clock id built from the thread id, on Windows through a thread handle from `OpenThread`. Both the one-shot and sampled tests pass on Windows and on Linux (WSL2 kernel 6.18, test binary cross-compiled and run there). Samples never decrease and never exceed the final reading.

**Not covered yet:**
- Linux has run only under WSL2, not on real node hardware. Check CPU time against wall time on an actual node before billing on it.
- The sampler is not yet wired into a long-running instance. The current runtime only has `_start` runs bounded by a timeout.
- A thread CPU clock counts only the thread the module runs on. Work the module hands to other threads would not appear. The current runtime has none.
