# Hermes v0.4 — Unified Data Plane

Dependency-free Node.js ECS execution kernel.

```bash
node Hermes.js
node Hermes.js 1000000 120
node Hermes.js --sweep
node Hermes.js --characterize
```

The characterization flag accepts either `--characterize` or `-characterize`.
It runs as a separate mode and exits after the report; a successful run returns
exit code 0 and ends with the correctness reminder.

The runtime supports TypedArray archetype chunks, compiled query plans,
generational IDs, typed command/event tables, deferred structural commits,
deterministic execution phases, system read/write metadata, dependency
inspection, change sets, external identities, replay, and optional profiling.

The primary frame API returns everything a consumer needs:

```js
const frame = world.step(dt);
renderer.apply(frame.changes);
```

`world.tick(dt)` remains a compatibility alias. A frame contains its tick, `dt`,
created/updated/structural/destroyed changes, dirty system batches, typed event
batches, budget metrics, and an optional deterministic checksum.

Use coarse-grained APIs for data-plane work:

```js
const handles = world.spawnMany(rackArchetype, rows, externalIds);
world.updateMany(handles, Temperature, values);

world.transaction((tx) => {
  tx.addMany(handles, Alarm, alarms);
  tx.removeMany(recovered, Alarm);
});
```

Snapshots provide schema validation hooks, external-ID binding, and partial
update ordering:

```js
world.ingestSnapshot(snapshot, {
  schemaVersion: 4,
  resolveArchetype: (record) => archetypes[record.type],
});

world.applyUpdates(telemetry);
```

Typed commands and events accept `grow`, `reject`, or `drop-oldest` overflow
policies. `world.startRecording()`, `world.input()`, `world.stopRecording()`,
and `world.replay()` provide deterministic input replay and checksum validation.

Optional profiling remains available:

```js
world.profile(true);
world.tick();
console.log(world.getProfile());
world.profile(false);
```

`world.profileReport(entityCount)` adds ns/entity, entities/sec, ticks/sec, and
60 Hz budget metrics. Structural APIs include `spawnDeferred`, `destroy`,
`add`, and `remove`; queued work is committed after system execution.

`chunkBytes`, `executionBatchSize`, and `migrationStrategy` are configurable
world options. Migration strategies are `grouped` (default), `individual`
(baseline), and `columnar` (source-chunk grouped bulk copies). Profiling is
disabled by default for uncontaminated throughput measurements.

See [CHARACTERIZATION.md](CHARACTERIZATION.md) for the canonical-vs-realistic
benchmark distinction and structural-engine findings. Existing v0.3 users can
follow [MIGRATION_V04.md](MIGRATION_V04.md).
