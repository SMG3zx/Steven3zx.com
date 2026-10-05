# Migrating from Hermes v0.3 to v0.4

Hermes v0.4 keeps the v0.3 entity, component, archetype, system, query, and
structural APIs. Existing `world.tick(dt)` calls continue to work and now return
the same frame result as `world.step(dt)`.

## Preferred frame loop

```js
const frame = world.step(dt);
renderer.apply(frame.changes);
```

The returned change set contains created, updated, structurally migrated, and
destroyed entities plus dirty chunk batches declared by system `write`
metadata. Use `world.filterChanges(changes, { query, components })` to derive a
consumer-specific view.

## Prefer bulk operations

Replace application-level loops over Hermes calls with `spawnMany`,
`updateMany`, `addMany`, `removeMany`, and `destroyMany`. Use
`world.transaction(tx => ...)` when several bulk operations must commit as one
ordered unit. Transaction callbacks receive a command-buffer facade; they do
not mutate the world until the callback returns successfully.

## External data

Use `ingestSnapshot` to validate a schema version, resolve records to
archetypes, and bind external IDs. Use `applyUpdates` for versioned partial
updates. Duplicate IDs, schema mismatches, missing components, and stale
updates are reported explicitly.

## Events and commands

Typed tables still grow by default. Pass `{ overflow: 'reject' }` or
`{ overflow: 'drop-oldest' }` as the fourth argument to `world.command` or
`world.event` for bounded ingestion. Inspect counters through `world.stats()`.

## Development safety

Set `{ development: true }` to turn stale handles and forbidden direct writes
during update phases into actionable errors. Production behavior remains
compatible with v0.3 unless an application opts into these checks.

## Deterministic diagnostics

Enable `{ checksum: true }` when frame checksums are required. Input recording
and replay are explicit through `startRecording`, `input`, `stopRecording`, and
`replay`; their cost is not present in the default kernel path.
