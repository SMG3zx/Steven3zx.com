# 05: Manual retry of a failed job

**What to build:** The Owner can start a new Import or WASM Build from a failed one. A new Import takes the same Project and commit. A new WASM Build takes the same Snapshot. The new job starts with zero resumes, so the failed job's interruptions don't count against it.

**Blocked by:** 04

**Status:** done

- [x] The Owner can start a new Import from a failed Import's Project and commit.
- [x] The Owner can start a new WASM Build from a failed WASM Build's Snapshot.
- [x] The new job starts with a resume count of zero.
- [x] The failed job remains in history, unchanged.
- [x] Only the Owner can start a retry.
- [x] Tested through the projects actor's message interface, with a real persistence actor on a temporary SQLite file.
