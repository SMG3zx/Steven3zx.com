# 06: Deleting a Project removes its Snapshots, WASM Builds, and Artifacts

**What to build:** Deleting a Project also deletes its Snapshots, WASM Builds, and Artifacts, so nothing is left behind with no owner-scoped way to reach it.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] Deleting a Project removes all of its Snapshots.
- [x] Deleting a Project removes all of its WASM Builds and their Artifacts.
- [x] Deleting a Project doesn't affect any other Project.
- [x] The stored Artifact content is removed, not just its record.
- [x] Tested through the projects actor's message interface, with a real persistence actor on a temporary SQLite file.
