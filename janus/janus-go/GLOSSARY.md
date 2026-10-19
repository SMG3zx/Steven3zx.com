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
The language with the most files in a Snapshot, as detected by its Scan. Examples: Go, Node, Python, Rust. A Language is buildable only when a WASM builder exists for it. Unbuildable Languages are still recorded, as a signal of which Projects Accounts want to build.
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
One execution of a WASM Build's Artifact.
_Avoid_: Execution

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
