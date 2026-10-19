Status: ready-for-agent

# Job resume, manual retry, and Account deletion

## Problem Statement

When the server restarts during an Import or WASM Build, the job is marked failed and never resumed. The Owner has to notice and start it again by hand, and any Staging Snapshot the Import had already written is thrown away.

Deleting an Account isn't possible. Its Projects, Provider Identities, and Sessions would remain with no way to remove them.

When an Account scans a Project in a language Janus can't build yet, that demand signal isn't recorded in a clear, consistent way. The rule for what counts as buildable isn't enforced.

## Solution

Interrupted Imports and WASM Builds resume automatically after a restart, up to three times. An Import resumes from its Staging Snapshot and restarts only the interrupted stage. A WASM Build restarts from scratch. A job that fails for good stays visible with its error, and the Owner can start a new job from the same Snapshot or Project.

Deleting an Account deletes its Projects, Provider Identities, and Sessions.

Every scanned Language is recorded on its Snapshot. A WASM Build is only allowed for a buildable Language.

## User Stories

1. As an Account owner, I want an Import that was interrupted by a server restart to resume automatically, so that I don't have to notice and restart it myself.
2. As an Account owner, I want a resumed Import to reuse the files it already stored, so that it doesn't re-download the whole repository.
3. As an Account owner, I want a resumed Import to restart the stage it was in when interrupted, so that completed stages aren't repeated.
4. As an Account owner, I want an Import to restart from the clone if its Staging Snapshot is missing or corrupt, so that a damaged partial import doesn't block me.
5. As an Account owner, I want an interrupted WASM Build to restart from scratch, so that I never get a build that's partly one attempt and partly another.
6. As an Account owner, I want a job that has been resumed three times to fail with an explanatory error, so that a job that keeps crashing the server doesn't run forever.
7. As an Account owner, I want a failed job to stay in my history with its error message, so that I can see what went wrong.
8. As an Account owner, I want to start a new Import from a failed one's Project and commit, so that I can try again once I've fixed the problem.
9. As an Account owner, I want to start a new WASM Build from a failed one's Snapshot, so that I can retry the build without re-importing.
10. As an Account owner, I want a new job started manually to get its own resume count, so that a previous job's interruptions don't count against it.
11. As an Account owner, I want a failed Import's Staging Snapshot to be discarded, so that no half-written Snapshot appears in my Project.
12. As an Account owner, I want my Snapshots to stay untouched when a later Import fails, so that the active Snapshot keeps working.
13. As an Account owner, I want to delete my Account, so that my data is removed.
14. As an Account owner, I want deleting my Account to remove all my Projects, so that nothing of mine remains.
15. As an Account owner, I want deleting my Account to remove its Snapshots, WASM Builds, and Artifacts, so that no stored output is left behind.
16. As an Account owner, I want deleting my Account to remove my Provider Identities, so that I can no longer sign in with them.
17. As an Account owner, I want deleting my Account to end my Sessions, so that no signed-in browser can keep acting for me.
18. As an Account owner, I want deleting a Project to remove its Snapshots, WASM Builds, and Artifacts, so that nothing is left behind.
19. As an Account owner, I want every scanned Language recorded on its Snapshot, so that the Project shows what it contains even when Janus can't build it yet.
20. As an Account owner, I want to start a WASM Build only for a buildable Language, so that I'm told clearly which Projects can be built.
21. As an Account owner, I want an unbuildable Language to still be recorded, so that Janus can see which languages people want.
22. As an Account owner, I want my Projects to stay private to me, so that no other Account can see or change them.
23. As an operator, I want resume attempts and failures recorded, so that I can see how often jobs are interrupted.
24. As an operator, I want interrupted jobs resumed on startup, so that a restart doesn't need manual cleanup.

## Implementation Decisions

- **Modules touched:**
  - The projects actor, which owns Import and WASM Build job lifecycle, including resume and manual retry.
  - The persistence actor, which owns job state, Staging Snapshot retention, and cascade deletion.
  - Application startup, which triggers the resume pass.
  - The session and identity records, which cascade on Account deletion.
  - The WASM Build path, which checks the buildable-Language rule.
- **Resume state:** each job tracks how many times it has been resumed, from zero to three. On the fourth interruption it fails with its error recorded.
- **Restart detection:** on startup, every job that was queued or running is treated as interrupted. Each one counts as one resume.
- **Import resume:** the job reuses its Staging Snapshot and restarts only the interrupted stage. If the Staging Snapshot is missing or corrupt, the Import restarts from the clone.
- **WASM Build resume:** the build restarts from scratch with no partial state.
- **Manual retry:** the Owner starts a new job. An Import takes a Project and commit. A WASM Build takes a Snapshot. The new job starts at zero resumes.
- **Failure retention:** failed jobs stay in the Owner's history with their error. Only the Staging Snapshot of a failed Import is discarded.
- **Account deletion:** removes the Account's Projects with their Snapshots, WASM Builds, and Artifacts, its Provider Identities, its GitHub Connection, and its Sessions.
- **Buildable-Language rule:** every Snapshot records its detected Language. A WASM Build is rejected unless a WASM builder exists for that Language. The recorded Language is the demand signal, so no separate table is needed.
- **Schema changes:** job records need a resume count. Staging Snapshot state must support resume. The exact schema is for the implementer to design.
- **Vocabulary:** use the terms in the domain glossary at `janus/janus-go/GLOSSARY.md`: Resume, Failed job, Staging Snapshot, Language, Artifact, Owner, Session, Provider Identity, GitHub Connection.

## Testing Decisions

- **What makes a good test:** test externally observable behaviour through the projects actor's message interface. Use a real persistence actor on a temporary SQLite file. Simulate a restart by stopping the actor system and reopening the same file. Do not assert on internal state.
- **Modules tested:**
  - The projects actor: resume, the three-resume cap, manual retry, and the buildable-Language gate.
  - The persistence actor: Staging Snapshot retention, failed-job retention, and cascade deletion.
- **Cases to cover:**
  - An interrupted Import resumes and reuses its Staging Snapshot.
  - A corrupt or missing Staging Snapshot causes a restart from the clone.
  - An interrupted WASM Build restarts from scratch.
  - A job interrupted a fourth time fails with an error recorded.
  - A manual retry starts at zero resumes.
  - A failed job keeps its error and history.
  - Deleting an Account removes its Projects, Provider Identities, and Sessions.
  - A WASM Build for an unbuildable Language is rejected, and its Language is still recorded.
- **Outer check:** the end-to-end projects test covers the HTTP path once the seam tests pass.
- **Prior art:**
  - `actors/persistence/projects_test.go`: builds a legacy schema in a temporary SQLite file and runs the persistence actor against it.
  - `actors/projects/import_test.go`: exercises the Import path.
  - `e2e/projects_test.go`: the outer end-to-end check.

## Out of Scope

- Builders for languages beyond Go and Rust.
- Sharing Projects between Accounts.
- Owner-initiated cancellation of a running job.
- Account deletion confirmation UX or email notification.
- Changes to the five-Snapshot retention limit.

## Further Notes

- **Decided:**
  - Account deletion is a single transaction, so no partial deletion is ever visible. Active jobs block it unless the Owner cancels them (see issue 07).
  - A job that was queued but never started counts as interrupted on restart and uses one resume, as the Restart detection decision above states. `recoverJobTable` in `actors/persistence/jobs.go` includes `queued`.
- **Current behaviour being replaced:** startup currently marks every queued or running import and WASM build as failed, and deletes incomplete Staging Snapshots. This spec replaces that.
- **Related decisions:** `janus/janus-go/docs/adr/0001-identity-matching-by-provider-id.md` records that sign-in identities are matched by provider ID, never by email. That bears on Account deletion, because deleting an Account removes its Provider Identities.
