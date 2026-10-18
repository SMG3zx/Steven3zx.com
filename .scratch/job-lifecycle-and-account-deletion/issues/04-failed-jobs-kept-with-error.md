# 04: Failed jobs stay visible, and failed Imports discard their Staging Snapshot

**What to build:** A job that fails for good stays in the Owner's history with its error message. A failed Import's Staging Snapshot is discarded, so no half-written Snapshot appears in the Project. The active Snapshot is never affected.

**Blocked by:** 02

**Status:** done

- [x] A failed Import or WASM Build appears in the Owner's job history with its error message.
- [x] A failed Import discards its Staging Snapshot.
- [x] A failed Import leaves the Project's existing Snapshots and active Snapshot unchanged.
- [x] A failed WASM Build leaves the Snapshot it compiled unchanged.
- [x] Tested through the projects actor's message interface, with a real persistence actor on a temporary SQLite file.
