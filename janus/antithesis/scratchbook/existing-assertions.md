---
sut_path: C:\Users\nerfs\Documents\Projects\Janus
commit: f899a3196ccaa07a107b372ec259cb9c8923e7df
updated: 2026-09-27
external_references: []
---

# Existing Antithesis assertions

The codebase scan searched Go, TypeScript, compose, documentation, and generated-source paths for Antithesis SDK imports and assertion forms including `assert_always`, `assert_sometimes`, `assert_reachable`, `assert_unreachable`, `Always`, `Sometimes`, `Reachable`, and `Unreachable`.

No Go/TypeScript Antithesis SDK import was found. The local Zig track now has
an account-free JSONL assertion writer in `Janus-Zig/src/observability/assertions.zig`
and native property coverage in `Janus-Zig/src/antithesis_tests.zig`; these are
local assertion events, not proof of a tenant-backed Antithesis run.

## Assumptions and Open Questions

### Assumptions

- Generated frontend files do not contain hidden Antithesis instrumentation; the search found none.

### Open Questions

- The appropriate tenant-backed SDK package/version still must be selected if
  the project later obtains an Antithesis account; `snouty` is currently
  unavailable locally.
