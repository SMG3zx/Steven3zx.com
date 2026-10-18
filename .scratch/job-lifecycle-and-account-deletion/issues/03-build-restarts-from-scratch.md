# 03: WASM Build restarts from scratch after restart

**What to build:** When the server restarts during a WASM Build, the build restarts from scratch with no partial state, so the Owner gets a complete build instead of one partly from an earlier attempt.

**Blocked by:** 01

**Status:** done

- [x] A WASM Build interrupted by a restart restarts from scratch and completes an Artifact.
- [x] No partial output from the interrupted attempt is reused.
- [x] Each resume increments the job's resume count, which is stored on the job.
- [x] Tested through the projects actor's message interface, with a real persistence actor on a temporary SQLite file.
