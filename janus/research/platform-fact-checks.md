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

## Provider figures re-checked on vendor pages (2026-10-19)

Fetched directly from each vendor's own page, for the figures ENG-57 leans on.

| Figure | Result |
|---|---|
| R2 Standard $0.015/GB-month, IA $0.01 plus $0.01/GB retrieval, Class A $4.50 / Class B $0.36 per million, free egress, free tier 10 GB, 1M A, 10M B | Confirmed on [R2 pricing](https://developers.cloudflare.com/r2/pricing/). IA operations are $9.00 and $0.90 per million, as recorded. |
| Lambda $0.0000166667/GB-second, $0.20 per million requests, free 1M requests and 400,000 GB-seconds | Confirmed on [Lambda pricing](https://aws.amazon.com/lambda/pricing/). |
| Fly volumes $0.15/GB-month | Confirmed on [Fly pricing](https://docs.fly.io/about/pricing). Fly egress is $0.02/GB in North America and Europe, $0.04 in Asia Pacific, Oceania and South America, $0.12 in Africa and India. Inbound is free. |
| Cloudflare Workers per-CPU-ms billing | Confirmed on [Workers pricing](https://developers.cloudflare.com/workers/platform/pricing/): 30M CPU-ms included, then $0.02 per million. Requests: 10M included, then $0.30 per million. Wall time and egress are not billed. |
| Fastly Compute per vCPU-ms | Confirmed on [Fastly pricing](https://www.fastly.com/pricing/): first 100M vCPU-ms free, then $0.05 per million, tiering down to $0.02. Requests $0.50 per million after 10M free. No separate duration rate. |
| Vercel Active CPU excludes I/O wait, memory billed for instance lifetime | Confirmed on [Vercel fluid compute pricing](https://vercel.com/docs/fluid-compute/pricing). Rates are per region: $0.128 per CPU-hour and $0.0106 per GB-hour in the cheapest US regions (iad1, pdx1, cle1). |
| Deno Deploy CPU billed only while code runs | Confirmed on [Deno pricing](https://deno.com/pricing): 10 hours free, 50 on Pro, 500 on Builder, overage $0.10/hour. |
| Azure Container Apps free grant of 180,000 vCPU-seconds, 360,000 GiB-seconds, 2M requests | Confirmed on [Azure pricing](https://azure.microsoft.com/en-us/pricing/details/container-apps/). This is the same allowance Janus gives (180k CPU-seconds, 360k GiB-seconds). The per-second rates did not load. |

**Still not confirmed from a vendor page:**
- **S3 Standard $0.023/GB-month and AWS egress $0.09/GB:** AWS's S3 and EC2 pages loaded without their price tables. Search results from mid-2026 third-party pages agree: $0.023 for the first 50 TB in us-east-1, and $0.09/GB after 100 GB free, for the next 10 TB. Read the AWS Pricing Calculator, or the Price List API, to confirm. Janus egress at $0.09/GB matches this figure.
- **Google Cloud Run:** the page text had no prices, and the fetch tool's answer came from background knowledge, not the page. Do not quote its rates.
- **Azure Container Apps per-second rates:** not shown in the fetched text.
- **Wasmer Edge:** third-party profile only, not checked.

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

## JavaScript and TypeScript to WASM (Node and Bun templates)

Researched 2026-10-19 for the two unbuildable templates in `janus/janus-go/actors/templates`. Sources are the Javy and ComponentizeJS READMEs and search results. **No tool was downloaded or run**, so everything below about output format is unconfirmed until a spike.

**What the Janus runner needs:** a core WASI module (preview 1) that exports `_start` and works with the runner's limits, because `ExecuteWASI` instantiates a core module with `wasmtime-go` v49 and calls `_start`. It does not run components.

| Option | What it is | Fit |
|---|---|---|
| [Javy](https://github.com/bytecodealliance/javy) (Bytecode Alliance) | Embeds the QuickJS interpreter in a Wasm module with your script. `javy build index.js -o index.wasm`. Reads stdin and writes stdout. Static output is at least 869 KB; dynamic linking gives 1 to 16 KB but needs a shared engine module at run time. | Best fit. Command-style, stdin and stdout match the runner. Interpreter, so fuel counts QuickJS operations, not script operations. |
| [ComponentizeJS](https://github.com/bytecodealliance/ComponentizeJS) | Embeds SpiderMonkey (StarlingMonkey), about 8 MB, and outputs a WASI 0.2 component. README calls it experimental. | Poor fit today. Needs component support in the runner, which `wasmtime-go` v49 use in this repo does not have. |
| Node or Bun themselves compiled to Wasm | Not a supported path. V8 and JavaScriptCore need a JIT, which Wasm does not allow. | Ruled out. |

**What every option loses:** Node and Bun APIs. Javy has no `fs`, `http`, or `crypto`; npm packages that need them fail. A Node or Bun project builds only if it is a pure script that reads stdin, writes stdout, and bundles its dependencies into one file.

**Proposal:** build the Node and Bun templates with Javy, in two steps inside the existing isolated builder container:
1. Bundle to one file: `bun build` for the Bun template, `esbuild` for the Node template. Both also strip TypeScript.
2. `javy build` the bundle to a `.wasm`, statically linked, so the Artifact stays one self-contained file.

Targets: `wasm32-wasip1`, like Rust. Entry file: the Entrypoint the Account confirmed.

**Open before building (a spike answers all three):**
- Does `javy build` output export `_start` and run under the runner's Wasmtime with fuel and epoch interruption on? The README does not say.
- Does the 128 MiB memory limit leave room for QuickJS plus a typical bundle?
- Fuel per second for QuickJS code, which will be far from the numbers in the fuel table above. CPU billing uses active CPU time, so billing is unaffected.

Also note for the template spec: users with `fs`, `http`, or npm-heavy projects will see a build failure with this approach, so the Scan should warn before the Owner confirms the Entrypoint.
