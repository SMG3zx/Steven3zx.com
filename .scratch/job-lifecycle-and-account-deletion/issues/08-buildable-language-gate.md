# 08: Buildable-Language gate

**What to build:** Every scanned Language is recorded on its Snapshot. A WASM Build is only accepted for a buildable Language, and the Owner is told clearly when a Language can't be built yet.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] Every Snapshot records the Language its Scan detected, including Languages Janus can't build yet.
- [x] A WASM Build request for a Language with no WASM builder is rejected with a clear message.
- [x] A WASM Build request for a buildable Language (Go or Rust) is accepted.
- [x] A rejected request doesn't change the Snapshot's recorded Language.
- [x] Tested through the projects actor's message interface, with a real persistence actor on a temporary SQLite file.
