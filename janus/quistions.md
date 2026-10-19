# Open questions for the Janus platform

These are the decisions still open on the Linear map [ENG-44](https://linear.app/gateswaysolutions/issue/ENG-44/wayfinder-map-janus-platform-from-source-to-production-runtime), plus a few loose ends from Janus Go. Each question has a recommended answer. Write "yes" next to a question to accept the recommendation, or write your own answer.

Questions marked **Mine** are facts I can look up or measure myself. They are listed so you know they are open, and they need no answer from you unless you want to change the approach.

## A. Building blocks inferred from code (ENG-51)

**A1. Which building blocks does the first spec infer?**
The ticket lists services, APIs, databases, cron jobs, pub/sub, object storage, caching, and load balancers. The first spec infers only the ones a WASM Artifact can use on today's platform: service/API (an HTTP handler), database (Turso), and cron job. Pub/sub, object storage, caching, and load balancers are recorded as later work.
Recommended: yes, infer service/API, database, and cron job first.

**A2. What code evidence triggers each block?**
Recommended triggers, read from the Snapshot without running any code:
- Service/API: an HTTP server import or framework dependency in the manifest or entry file.
- Database: a Turso, SQLite, or libSQL client dependency, or a connection URL in config.
- Cron job: a cron library dependency, or a schedule file in a known location.
Recommended: yes, those three kinds of evidence.

**A3. Does the Owner confirm inferred blocks before Deploy?**
Inference is a suggestion, like the Scan's Entrypoint candidates. The Owner confirms or edits the list, and Deploy uses the confirmed list.
Recommended: yes, the Owner confirms, as with the Entrypoint.

**A4. What happens to a database block at Deploy?**
A database block gets a Turso database per Deployment, created by Janus, with its connection details passed to the instance as environment values. Storage is billed under the Storage layer (ENG-57).
Recommended: yes, one Turso database per Deployment that needs one.

**A5. What happens to a cron job block at Deploy?**
The Distributed Network and the local agent both start the instance on schedule instead of keeping it running. Each run is billed like any other run.
Recommended: yes, scheduled start with the same billing.

## B. Purchased credits through Stripe (ENG-60)

**B1. What does an Account buy?**
Credits in fixed packs (for example $10, $25, $100), where one credit is one US dollar of usage at the ENG-57 rates. Stripe Checkout handles the payment.
Recommended: yes, dollar-denominated packs through Stripe Checkout.

**B2. How does usage draw the balance down?**
Each 60-second usage record from a node (ENG-56) is priced with the ENG-57 rates and subtracted from the balance. The free allowance is used first.
Recommended: yes, draw down per usage record, free allowance first.

**B3. What happens when the balance reaches zero?**
Running deployments on the Distributed Network get a warning at 10% remaining, and are stopped when the balance reaches zero. Their Artifacts and settings are kept so they can be started again after a top-up.
Recommended: yes, warn at 10%, stop at zero, keep everything.

**B4. Does the free allowance apply on the Distributed Network?**
ENG-57 set a free allowance of 180,000 CPU-seconds and 360,000 GiB-seconds a month. If the Distributed Network needs credits, there are two readings: the free allowance only covers local runs (which are unbilled anyway), or an Account with any purchased credits also gets the free allowance on the Distributed Network.
Recommended: the second reading. Accounts with purchased credits get the free allowance on the Distributed Network, and Accounts without credits cannot use it.

**B5. Do credits expire, and are they refundable?**
Recommended: credits do not expire, and unused credits can be refunded within 30 days of purchase through Stripe.

**B6. What does Stripe store versus Janus?**
Janus stores the balance and usage ledger in Turso. Stripe holds payment details. Janus never sees card numbers.
Recommended: yes.

## C. Platform decisions still in the fog

**C1. Multi-node scaling for one deployment.**
At launch each deployment runs on one node. Should scaling a deployment across several nodes be a ticket now or stay deferred until the first spec ships?
Recommended: stay deferred.

**C2. How are unbilled local-run stats shown to the user?**
Local runs report fuel, peak memory, duration, network bytes, and status. Recommended display: the same units as billing (CPU-seconds, GiB-seconds, GB), with a clear "not billed" label, in a per-project usage page.
Recommended: yes.

**C3. Is `janus-web` reused or replaced?**
The stack decision was Go, templ, and Turso, with no TypeScript. That points to replacing `janus-web`, with `janus-go` serving the web UI with templ.
Recommended: replace `janus-web`. Keep it untouched in the repo as history.

**C4. Origin-side TLS between Cloudflare and the edge proxy.**
The wildcard covers the public side only. For the Cloudflare-to-origin hop, use a Cloudflare Origin CA certificate on the edge proxy, and set Cloudflare SSL mode to Full (strict).
Recommended: yes.

**C5. Apex (bare) domains for BYO domains.**
Needs a paid Cloudflare add-on. Recommended: stay deferred until there is paid revenue.

**C6. Paid Cloudflare for SaaS overage beyond 100 hostnames.**
Recommended: stay deferred. When the 100 free hostnames are used, new BYO domains are refused with a clear message.

**C7. The fuel-to-CPU-second constant (Mine).**
I would measure it by running a reference workload through Wasmtime and recording fuel against wall time. Tell me if you want a specific reference workload other than the existing sample modules in `janus-go`.

**C8. Storage price check against S3 and R2 (Mine).**
I will compare current storage prices and replace the $0.15/GiB-month placeholder.

**C9. Re-verify the provider figures in the pricing research (Mine).**
Several figures came from third-party pages. I will re-check them on each provider's own pricing page.

## D. Janus Agent

**D1. How does the agent start and stop?**
Beyond the optional service install, how should a user stop an agent from running deployments temporarily? Recommended: a `janus-agent pause` and `janus-agent resume` command, plus a pause button in the web UI that sends the same request.
Recommended: yes.

**D2. Exact Headscale pre-auth key request (Mine).**
I will confirm the request body for creating a single-use, one-hour key against the Headscale API before the enrollment spec is written down.

## E. Janus Go loose ends

**E1. Language tie-break.**
When two languages have the same number of files in a Snapshot, the code picks alphabetically. Earlier you agreed to an empty Language on a tie, which conflicts with the code. Which should it be?
Recommended: keep the alphabetical tie-break, since an empty Language hides the Project from language signals.

**E2. `LINEAR_TEAM_OVERVIEW_ANALYSIS.md` is deleted in the working tree.**
It is not mine and I left it alone. Should I commit the deletion, restore the file, or leave it?
Recommended: leave it. It is outside this work.

**E3. Janus Go commits are not pushed.**
Several commits on `Janus-Go` exist only locally. Push them to `origin`?
Recommended: yes, push when you are ready. I will not push without your say.

## Answers (2026-10-19)

Everything above was accepted as recommended, except:

- **C3:** replace `janus-web` and remove it from the repo. Done.
- **C5:** apex domains are parked in `janus/cold-storage/`, which agents do not scan unless asked.
- **E2:** commit the deletion of `LINEAR_TEAM_OVERVIEW_ANALYSIS.md`. Done.
- **E3:** push the `Janus-Go` commits.
