# 01: Import resumes after restart

**What to build:** When the server restarts during an Import, the Import resumes from its Staging Snapshot and restarts only the interrupted stage. The Owner doesn't have to start it again.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] An Import interrupted by a restart resumes automatically on startup and completes into a Snapshot.
- [x] A resumed Import reuses the files already stored in its Staging Snapshot instead of storing them again. The Staging Snapshot keeps a manifest of every file in the commit, so a resumed Import knows what is still missing.
- [x] Stages completed before the interruption are not repeated. A resumed Import with a manifest skips the scan. It clones only when the manifest lists files that are not stored yet, and it clones nothing when every file is stored.
- [x] If the Staging Snapshot is missing or corrupt, the Import restarts from the clone.
- [x] Each resume increments the job's resume count, which is stored on the job.
- [x] Tested through the projects actor's message interface, with a real persistence actor on a temporary SQLite file and a restart simulated by reopening that file.

**Decided:** a job that was queued but never started counts as interrupted on restart, and is handled by the same state machine.
