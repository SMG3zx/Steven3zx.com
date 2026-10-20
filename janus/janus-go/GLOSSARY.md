# Janus Go

Janus Go is the Go application behind Janus: it lets an Account import repositories into Projects and build and run them as WebAssembly.

## Accounts and projects

**Account**:
A person's place in Janus, where they keep all their projects. Sign-in methods attach to an Account. Deleting an Account also deletes its Projects, Provider Identities, and GitHub Connection, and its Sessions stop working. Deletion is refused while the Account has unfinished imports or WASM builds, unless the Owner chooses to cancel them first.
_Avoid_: User, profile

**Provider Identity**:
A sign-in method attached to an Account. Each one names its provider: local, Google, or GitHub. Only the local identity carries a password.
_Avoid_: Login, credentials, sign-in link

**GitHub Connection**:
The stored access token that lets a Project import from the Account's GitHub repositories. It is separate from the GitHub Provider Identity used to sign in. Disconnecting removes the token and leaves the identity in place.
_Avoid_: GitHub login, GitHub token

**Owner**:
The Account that owns a Project. Only the Owner can see or change it. A Project is private to its Owner.
_Avoid_: Creator, author

**Project**:
A Janus workspace owned by exactly one Account, optionally linked to a Repository. Deleting a Project also deletes its Snapshots, WASM Builds, and Artifacts.
_Avoid_: Repo, workspace

**Repository**:
An external source repository, such as a GitHub repo, that a Project refers to. A Project references a Repository but does not own it.
_Avoid_: Repo

## Source

**Import**:
A job that fetches one commit of a Repository into a Project. It includes a Scan before the Snapshot is completed.
_Avoid_: Sync, clone

**Scan**:
The stage of an Import that detects the Snapshot's language and suggests Entrypoint candidates, without running any repository code.
_Avoid_: Analysis, inspection

**Language**:
The language with the most files in a Snapshot, as detected by its Scan. When two languages tie, the alphabetically first one wins. Examples: Go, Python, Rust, JavaScript/TypeScript (Node), JavaScript/TypeScript (Bun). The runtime appears in parentheses when one is detected. A Language is buildable only when a WASM builder exists for it. Unbuildable Languages are still recorded, as a signal of which Projects Accounts want to build.
_Avoid_: Stack, runtime

**Staging Snapshot**:
A Snapshot still being written during an Import. It isn't a Snapshot until the Import completes. A resumed Import reuses it. It is discarded only when the Import fails for good.
_Avoid_: Partial snapshot, draft

**Snapshot**:
The stored result of a completed Import: the source files of one commit. A Project keeps up to five Snapshots, and exactly one is active.
_Avoid_: Revision, version

**Entrypoint**:
The entry file or package, confirmed by the Account, that a WASM Build compiles. Each Snapshot has its own Entrypoint.
_Avoid_: Entry point, main

## Builds and runs

**Template**:
A premade recipe for one kind of Project. It names the Language and runtime it covers, the manifest file that marks it, the Targets and build command, how its Entrypoint is found, and the resource limits a WASM Run starts with. Four ship first: Go, Rust, JavaScript/TypeScript on Node, and JavaScript/TypeScript on Bun. Go and Rust are buildable; the two JavaScript templates are detected by the Scan but have no WASM builder yet.
_Avoid_: Preset, starter

**WASM Build**:
A job that compiles one Snapshot to an Artifact. It records the Snapshot it compiled.
_Avoid_: Compile job

**Target**:
The WebAssembly target a WASM Build compiles for: `wasm32-unknown-unknown`, `wasm32-wasip1`, or `wasip1-wasm`.
_Avoid_: Platform, triple

**Artifact**:
The compiled `.wasm` module that a WASM Build produced, kept so it can be run. A WASM Run executes an Artifact.
_Avoid_: Output, binary

**WASM Run**:
One execution of a WASM Build's Artifact. It records its Usage.
_Avoid_: Execution

**Usage**:
What a WASM Run consumed, measured by the host: the fuel it spent, the peak size of its linear memory, how long it ran, and the active CPU time it used. Active CPU time leaves out time spent waiting, and it is what the CPU layer bills. Fuel is exact and repeats for the same module and input. Usage is recorded for every run, and a run that fails or runs out of fuel still reports what it spent.
_Avoid_: Metrics, billing

## Deployments

**Check**:
The result of running a Template's check and test commands against one WASM Build, automatically after the build succeeds, in the same isolated builder containers. Owners cannot edit the commands. A Project with no tests passes on the compile-level check alone, and its Check records "no tests found". A new Snapshot or WASM Build has no Check until its own run finishes.
_Avoid_: Test run, CI

**Deploy**:
The Owner's action that creates a Deployment from a WASM Build whose latest Check passed. If the Check failed, the Owner can choose "deploy anyway", which is recorded on the Deployment and as an Audit Event.
_Avoid_: Release, publish

**Deployment**:
One Artifact running on one target, the Owner's machine through the Janus Agent in M1. A Project has one live Deployment. A redeploy creates a new Deployment that replaces the live one once it is healthy; the live one keeps serving until then. A Project keeps its last five stopped Deployments for rollback. Rollback deploys one of those Artifacts as a new Deployment, and never revives the old record. A Deployment is served per request: the Agent starts the module for each HTTP request, passes the request in through environment and stdin, and reads the response from stdout. Stopping a Deployment keeps its record and Usage, keeps its Artifact while its WASM Build exists, and retires its Temporary Domain for good.
_Avoid_: Release, instance, rollout

**Deployment states**:
`queued`, `starting`, `healthy`, `failed`, `stopped`. A Deployment moves queued to starting to healthy to stopped; queued and starting can also move to failed, and a failed Deployment never becomes healthy. A stopped Deployment carries a reason: replaced, removed, rolled back, expired, or account deleted. "Agent offline" is not a state: the Deployment stays healthy, the edge answers 503, and the UI shows the Agent offline.

**Healthy**:
A Deployment is healthy once its Agent has fetched the Artifact, verified its hash, compiled it, and one probe invocation (`GET /`) ends without a trap, timeout, or running out of fuel. The HTTP status the module returns is not judged.

**Request limits**:
Each request starts with the Template's fuel, memory and stack limits and a 30-second wall-clock timeout. An Agent runs up to 16 requests at once; beyond that the edge answers 503. Request and response bodies are capped at 10 MiB. A limit hit returns a 5xx and still records Usage. These are starting values.

**Temporary Domain**:
The public name of a Project's live Deployment: a random 8-character label under `steven3zx.com`. The Project gets one at its first Deploy, and it moves to each new Deployment once that one is healthy, so the URL stays stable across redeploys and rollbacks. It expires 7 days after the last successful Deploy. When it expires or the Deployment is removed, the name is retired and never reissued, and the next Deploy gets a new one. While a first Deployment is starting, the domain answers 503 with a short "deploying" page.
_Avoid_: Preview URL, subdomain

**Deploy target**:
The Agent a Deploy goes to. The Owner picks it at Deploy time: preselected when exactly one Agent is enrolled, otherwise the last used. A Deployment is pinned to that Agent. Deploy is refused if the Account has no enrolled, online Agent.

**Deploy gating**:
Deploy is disabled while the Check for that WASM Build is running. A Check that itself errors (container crash, timeout) counts as failed, keeps its error, and can be re-run; "deploy anyway" is then available. A Project has one in-flight Deploy: a second one is refused until the first is healthy or failed. The Owner can stop the in-flight one (stopped, reason removed), and the live Deployment keeps serving throughout.

**Removing an Agent or expiry**:
Removing an Agent stops every Deployment pinned to it (reason removed), and the domain answers 503 until the next Deploy. Expiry stops the live Deployment (reason expired) and retires the name. Neither deletes the Artifact or Usage. The UI warns the Owner one day before expiry.

**Failed Deployment**:
A Deployment that ended in an error, kept with its message. It is not resumed. If the control plane restarts mid-Deploy, it reconciles with the Agent and marks the Deployment healthy or failed. The Owner starts a new Deploy from the same WASM Build, and the previous live Deployment keeps serving meanwhile.

## Agents

**Agent**:
The Janus program on an Owner's machine that runs a Deployment's module. It joins the Janus tailnet under the tag `tag:acct-<owner id>` and accepts connections only from the control plane. It opens no listener on the internet. An Agent is listed with a status: online, offline (its node joined but the tailnet reports it down), pending (enrolled, but no node has joined with its key yet), or unknown (the tailnet could not be asked).
_Avoid_: Worker, runner, node (a node is the Agent's presence on the tailnet)

**Enrollment Code**:
A single-use code the web UI shows an Owner once, valid for 15 minutes, that the Agent's command line exchanges for a pre-auth key. Janus keeps only a hash of it. An unknown, used or expired code is refused in the same words, so a guess learns nothing. A code is used up even if enrolling fails afterwards, so the Owner asks for a new one.
_Avoid_: Token, invite, pairing code

**Tailnet**:
The one private network the Agents and the control plane join, run by Headscale. It is default-deny: the control plane may open connections to any tagged node, and nothing else may. An Agent cannot reach the control plane or another Account's Agent. Removing an Agent deletes its node, so it can no longer be reached, and expires its pre-auth key.
_Avoid_: VPN, mesh

## Jobs

**Resume**:
Continuing an Import or WASM Build after a server restart interrupted it. A job that was still queued at restart counts as interrupted too. An Import resumes from its Staging Snapshot and restarts only the interrupted stage. A WASM Build restarts from scratch. A job resumes at most three times, then fails.
_Avoid_: Retry, restart

**Cancelled job**:
An Import or WASM Build stopped because its Owner deleted the Account and chose to cancel active jobs. A cancelled job never resumes.
_Avoid_: Aborted, stopped

**Failed job**:
An Import or WASM Build that ended in an error, kept with its error message so the Owner can see what went wrong. The Owner can start a new job from the same Snapshot or Project. The new job gets its own resume count.
_Avoid_: Dead job, error record

## Access and records

**Session**:
The signed-in state an Account holds in the browser, valid until it expires. Logging out clears it, but a copied token stays valid until expiry. Once its Account is deleted, the token is rejected.
_Avoid_: Login, token

**Audit Event**:
A structured record of an application action. It never includes passwords, OAuth codes, tokens, or raw emails.
_Avoid_: Log entry, audit log
