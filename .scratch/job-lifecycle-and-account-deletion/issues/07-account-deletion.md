# 07: Deleting an Account, end to end

**What to build:** The Owner can delete their Account. The deletion removes its Projects (with their Snapshots, WASM Builds, and Artifacts), its Provider Identities, its GitHub Connection, and its Sessions. No deletion path exists yet, so this ticket adds the entry point.

**Blocked by:** 06

**Status:** done, with deviations (see below)

- [x] The Owner can delete their Account through a new endpoint.
- [x] Deleting the Account removes all of its Projects, with their Snapshots, WASM Builds, and Artifacts.
- [x] Deleting the Account removes all of its Provider Identities.
- [ ] Deleting the Account removes its GitHub Connection. NOT ASSERTED: cascades by foreign key, but no test checks it.
- [x] Deleting the Account ends all of its Sessions, so a signed-in browser can no longer act for it.
- [ ] Another Account's Projects and identities are unaffected.
- [x] Tested through the projects actor's message interface, with a real persistence actor on a temporary SQLite file.

**Decided:** the deletion runs in one transaction, so no partial deletion is visible. Active jobs block it unless the Owner chooses to cancel them.
