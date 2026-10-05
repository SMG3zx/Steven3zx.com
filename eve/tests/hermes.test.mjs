import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const { Hermes } = require('../Hermes/Hermes.js');

test('Hermes v0.2 profiles deterministic phases and dependencies', () => {
  const world = new Hermes.World({
    entityCapacity: 8,
    chunkBytes: 64 * 1024,
    executionBatchSize: 2,
  });
  const Position = world.component('Position', { x: Float64Array });
  const archetype = world.archetype(Position);
  const order = [];
  world.system({
    name: 'Input',
    phase: Hermes.Phase.INPUT,
    query: [Position],
    read: [],
    write: [Position],
    run() {
      order.push('input');
    },
  });
  world.system({
    name: 'Update',
    phase: Hermes.Phase.UPDATE,
    after: 'Input',
    query: [Position],
    read: [Position],
    write: [Position],
    run() {
      order.push('update');
    },
  });
  world.spawn(archetype, { Position: { x: 1 } });
  world.profile(true);
  world.tick();
  assert.deepEqual(order, ['input', 'update']);
  assert.equal(world.getProfile().samples, 1);
  assert.equal(typeof world.getProfile().runtime, 'number');
  assert.equal(world.profileReport(1).systemMetrics.Input.percent > 0, true);
  assert.equal(world.dependencies[0].conflict, true);
  assert.equal(world.chunkBytes, 64 * 1024);
  assert.equal(world.query(Position).batches.length, 1);
  assert.equal(world.query(Position).executionBatches.length, 1);
  const deferred = world.spawnDeferred(archetype, { Position: { x: 2 } });
  assert.equal(deferred, 0);
  world.tick();
  assert.equal(world.entities.next, 2);
  const Armor = world.component('Armor', { value: Uint8Array });
  const handle = world.entities.handle(0);
  world.add(handle, Armor, { value: 7 });
  world.tick();
  assert.equal(world.location(handle).archetype.has(Armor), true);
  const armorLocation = world.location(handle);
  const armorColumn =
    armorLocation.chunk.columns[armorLocation.archetype.componentIndex.get(Armor.id)].value;
  assert.equal(armorColumn[armorLocation.row], 7);
  world.remove(handle, Armor);
  world.tick();
  assert.equal(world.location(handle).archetype.has(Armor), false);
});

test('Hermes preserves generations, swap-remove, typed tables, and plan invalidation', () => {
  const world = new Hermes.World({ entityCapacity: 8 });
  const P = world.component('P', { x: Float64Array });
  const a = world.archetype(P);
  const first = world.spawn(a, { P: { x: 1 } });
  const second = world.spawn(a, { P: { x: 2 } });
  const plan = world.query(P);
  assert.equal(plan.batches[0].chunk.count, 2);
  world.destroy(first);
  world.tick();
  assert.equal(world.entities.resolve(first), Hermes.EMPTY);
  assert.equal(world.location(second).row, 0);
  const replacement = world.spawn(a, { P: { x: 3 } });
  assert.notEqual(replacement, first);
  const Damage = world.command('Damage', { target: Uint32Array, amount: Float32Array });
  Damage.push({ target: 0, amount: 4 });
  assert.equal(Damage.columns.amount[0], 4);
  const Hit = world.event('Hit', { target: Uint32Array });
  Hit.push({ target: 0 });
  assert.equal(Hit.columns.target[0], 0);
  const Q = world.query(P);
  const Armor = world.component('Armor', { value: Uint8Array });
  world.add(second, Armor, { value: 1 });
  world.tick();
  Q.refresh();
  assert.equal(Q.version, world.topologyVersion);
  assert.equal(world.location(second).archetype.has(Armor), true);
});

for (const migrationStrategy of ['individual', 'grouped', 'columnar']) {
  test(`Hermes ${migrationStrategy} migration preserves partial chunk rows`, () => {
    const world = new Hermes.World({
      entityCapacity: 128,
      chunkBytes: 256,
      migrationStrategy,
    });
    const Position = world.component('Position', { x: Float64Array });
    const Armor = world.component('Armor', { value: Uint8Array });
    const source = world.archetype(Position);
    const target = world.archetype(Position, Armor);
    const handles = [];
    for (let i = 0; i < 40; i++) handles.push(world.spawn(source, { Position: { x: i } }));
    for (let i = 0; i < handles.length; i += 2) world.add(handles[i], Armor, { value: i + 1 });
    world.tick();
    for (let i = 0; i < handles.length; i++) {
      const location = world.location(handles[i]);
      assert.equal(location.archetype, i % 2 === 0 ? target : source);
      assert.equal(location.chunk.columns[0].x[location.row], i);
      if (i % 2 === 0) assert.equal(location.chunk.columns[1].value[location.row], i + 1);
    }
    assert.equal(world.lastStructuralStats.strategy, migrationStrategy);
    assert.equal(world.lastStructuralStats.entities, 20);
    assert.equal(world.lastStructuralStats.groups, 1);
    assert.equal(world.lastStructuralStats.bytesCopied > 0, true);
  });
}

test('Hermes v0.4 steps once and returns bulk change sets', () => {
  const world = new Hermes.World({ entityCapacity: 64 });
  const Position = world.component('Position', { x: Float32Array });
  const Velocity = world.component('Velocity', { x: Float32Array });
  const moving = world.archetype(Position, Velocity);
  const handles = world.spawnMany(moving, [
    { Position: { x: 1 }, Velocity: { x: 2 } },
    { Position: { x: 3 }, Velocity: { x: 4 } },
  ]);
  world.system({
    name: 'Movement',
    query: [Position, Velocity],
    write: [Position],
    run(columns, count, dt) {
      for (let i = 0; i < count; i++) columns[0].x[i] += columns[1].x[i] * dt;
    },
  });
  const frame = world.step(0.5);
  assert.equal(frame.tick, 1);
  assert.equal(frame.changes.created.length, 2);
  assert.equal(frame.changes.dirtyBatches.length, 1);
  assert.equal(world.location(handles[0]).chunk.columns[0].x[world.location(handles[0]).row], 2);
  assert.equal(world.compileQuery(Position, Velocity), world.query(Position, Velocity));
  assert.equal(world.stats().frame.tick, 1);
  assert.equal(world.tick().tick, 2);
  assert.equal(world.destroyMany(handles), 2);
  const destroyed = world.step();
  assert.equal(destroyed.changes.destroyed.length, 2);
});

test('Hermes v0.4 ingests snapshots and rejects stale partial updates', () => {
  const world = new Hermes.World({ entityCapacity: 64, schemaVersion: 4 });
  const Temperature = world.component('Temperature', { celsius: Float32Array });
  const rack = world.archetype(Temperature);
  const result = world.ingestSnapshot(
    {
      schemaVersion: 4,
      entities: [
        { id: 'rack-1', type: 'rack', version: 10, components: { Temperature: { celsius: 20 } } },
        { id: 'rack-2', type: 'rack', version: 10, components: { Temperature: { celsius: 21 } } },
      ],
    },
    { resolveArchetype: () => rack },
  );
  assert.equal(result.count, 2);
  assert.notEqual(world.resolveExternal('rack-1'), null);
  assert.deepEqual(
    world.applyUpdates([
      { id: 'rack-1', component: 'Temperature', version: 9, values: { celsius: 99 } },
      { id: 'rack-2', component: Temperature, version: 11, values: { celsius: 24 } },
      { id: 'rack-3', component: Temperature, version: 11, values: { celsius: 25 } },
    ]),
    { applied: 1, stale: 1, missing: 1 },
  );
  const handle = world.resolveExternal('rack-2');
  const location = world.location(handle);
  assert.equal(location.chunk.columns[0].celsius[location.row], 24);
  assert.throws(() => world.ingestSnapshot({ schemaVersion: 3, entities: [] }), /does not match/);
  const strictWorld = new Hermes.World({ entityCapacity: 4, schemaVersion: 4 });
  const StrictTemperature = strictWorld.component('Temperature', { celsius: Float32Array });
  const strictRack = strictWorld.archetype(StrictTemperature);
  assert.throws(
    () =>
      strictWorld.ingestSnapshot(
        { schemaVersion: 4, entities: [{ id: 'missing-site', components: {} }] },
        {
          resolveArchetype: () => strictRack,
          requiredFields: ['site'],
          strictComponents: true,
        },
      ),
    /missing required field site/,
  );
  assert.equal(
    strictWorld.ingestSnapshot(
      { schemaVersion: 3, entities: [] },
      { schemaVersion: 4, migrate: (snapshot) => ({ ...snapshot, schemaVersion: 4 }) },
    ).schemaVersion,
    4,
  );
});

test('Hermes v0.4 transactions buffer operations until callback succeeds', () => {
  const world = new Hermes.World({ entityCapacity: 32 });
  const Position = world.component('Position', { x: Float32Array });
  const source = world.archetype(Position);
  assert.throws(() =>
    world.transaction((transaction) => {
      transaction.spawnMany(source, [{ Position: { x: 1 } }]);
      throw new Error('cancel');
    }),
  );
  assert.equal(world.entities.next, 0);
  world.transaction((transaction) => {
    transaction.spawnMany(source, [{ Position: { x: 2 } }], ['external-1']);
  });
  assert.equal(world.entities.next, 1);
  assert.notEqual(world.resolveExternal('external-1'), null);
});

test('Hermes v0.4 applies typed-table backpressure policies', () => {
  const world = new Hermes.World({ entityCapacity: 4 });
  const rejected = world.event('Rejected', { value: Uint8Array }, 2, {
    overflow: 'reject',
  });
  assert.equal(rejected.push({ value: 1 }), 0);
  assert.equal(rejected.push({ value: 2 }), 1);
  assert.equal(rejected.push({ value: 3 }), -1);
  assert.equal(rejected.stats().rejected, 1);
  assert.equal(world.step().events.Rejected.stats.rejected, 1);
  const rolling = world.command('Rolling', { value: Uint8Array }, 2, {
    overflow: 'drop-oldest',
  });
  rolling.push({ value: 1 });
  rolling.push({ value: 2 });
  rolling.push({ value: 3 });
  assert.deepEqual([...rolling.columns.value.slice(0, rolling.count)], [2, 3]);
  assert.equal(world.stats().commands.Rolling.dropped, 1);
});

test('Hermes v0.4 records and deterministically replays inputs', () => {
  const createWorld = () => {
    const world = new Hermes.World({ entityCapacity: 8, checksum: true });
    const Position = world.component('Position', { x: Float32Array });
    const positioned = world.archetype(Position);
    world.spawnMany(positioned, [{ Position: { x: 1 } }], ['entity-1']);
    return { world, Position };
  };
  const original = createWorld();
  const update = (payload, world) =>
    world.applyUpdates([
      {
        id: payload.id,
        component: 'Position',
        version: payload.version,
        values: { x: payload.x },
      },
    ]);
  original.world.startRecording();
  original.world.input('position', { id: 'entity-1', version: 1, x: 42 }, update);
  original.world.step();
  const log = original.world.stopRecording();
  const replayed = createWorld();
  const result = replayed.world.replay(log, { position: update });
  assert.equal(result.checksum, original.world.checksum());
  assert.equal(result.tick, 1);
});

test('Hermes v0.4 replay reproduces structural state', () => {
  const createWorld = () => {
    const world = new Hermes.World({ entityCapacity: 8, checksum: true });
    const Position = world.component('Position', { x: Float32Array });
    const Alarm = world.component('Alarm', { severity: Uint8Array });
    const positioned = world.archetype(Position);
    world.spawnMany(positioned, [{ Position: { x: 1 } }], ['rack-1']);
    return { world, Alarm };
  };
  const applyAlarm = (payload, world) => {
    const handle = world.resolveExternal(payload.id);
    world.add(handle, world.componentByName.get('Alarm'), { severity: payload.severity });
  };
  const original = createWorld();
  original.world.startRecording();
  original.world.input('alarm', { id: 'rack-1', severity: 2 }, applyAlarm);
  original.world.step();
  const log = original.world.stopRecording();
  const replayed = createWorld();
  replayed.world.replay(log, { alarm: applyAlarm });
  assert.equal(replayed.world.checksum(), original.world.checksum());
  assert.equal(
    replayed.world.location(replayed.world.resolveExternal('rack-1')).archetype.has(replayed.Alarm),
    true,
  );
});

test('Hermes v0.4 reports frame budgets and development stale handles', () => {
  let warning = null;
  const world = new Hermes.World({
    entityCapacity: 4,
    development: true,
    frameBudgetMs: 0,
    onFrameBudgetExceeded: (stats) => {
      warning = stats;
    },
  });
  const Position = world.component('Position', { x: Float32Array });
  const positioned = world.archetype(Position);
  const handle = world.spawn(positioned, { Position: { x: 1 } });
  world.destroy(handle);
  world.step();
  assert.equal(warning.overBudget, true);
  assert.throws(() => world.destroy(handle), /stale entity/);
});

test('Hermes v0.4 filters changes by compiled query and component', () => {
  const world = new Hermes.World({ entityCapacity: 16 });
  const Position = world.component('Position', { x: Float32Array });
  const Temperature = world.component('Temperature', { value: Float32Array });
  const positioned = world.archetype(Position);
  const monitored = world.archetype(Position, Temperature);
  const handles = [
    world.spawn(positioned, { Position: { x: 1 } }),
    world.spawn(monitored, { Position: { x: 2 }, Temperature: { value: 20 } }),
  ];
  world.updateMany(handles, Position, [{ x: 3 }, { x: 4 }]);
  world.updateMany([handles[1]], Temperature, [{ value: 21 }]);
  const frame = world.step();
  const filtered = world.filterChanges(frame.changes, {
    query: world.compileQuery(Temperature),
    components: [Temperature],
  });
  assert.deepEqual(filtered.created, [handles[1]]);
  assert.equal(filtered.updated.length, 1);
  assert.equal(filtered.updated[0].component, Temperature.id);
});

test('Hermes v0.4 reuses structural migration workspaces', () => {
  const world = new Hermes.World({ entityCapacity: 64 });
  const Position = world.component('Position', { x: Float32Array });
  const Armor = world.component('Armor', { value: Uint8Array });
  const source = world.archetype(Position);
  const handles = world.spawnMany(
    source,
    Array.from({ length: 20 }, (_, x) => ({ Position: { x } })),
  );
  world.addMany(
    handles,
    Armor,
    handles.map(() => ({ value: 1 })),
  );
  world.step();
  const firstCapacity = world.stats().pools.structuralRecords;
  world.removeMany(handles, Armor);
  world.step();
  assert.equal(world.stats().pools.structuralRecords, firstCapacity);
  assert.equal(firstCapacity, handles.length);
});

test('Hermes v0.4 development mode rejects direct mutation in update systems', () => {
  const world = new Hermes.World({ entityCapacity: 8, development: true });
  const Position = world.component('Position', { x: Float32Array });
  const positioned = world.archetype(Position);
  const handle = world.spawn(positioned, { Position: { x: 1 } });
  world.system({
    name: 'IllegalMutation',
    query: [Position],
    run() {
      world.updateMany([handle], Position, [{ x: 2 }]);
    },
  });
  assert.throws(() => world.step(), /not allowed during UPDATE/);
  assert.equal(world.currentPhase, null);
});

test('Hermes v0.4 development mode validates commands and component access', () => {
  const world = new Hermes.World({ entityCapacity: 8, development: true });
  const Position = world.component('Position', { x: Float32Array });
  const positioned = world.archetype(Position);
  world.spawn(positioned, { Position: { x: 1 } });
  const command = world.command('Move', { x: Float32Array });
  assert.throws(() => command.push({}), /missing x/);
  world.system({
    name: 'UndeclaredWrite',
    query: [Position],
    read: [Position],
    write: [],
    run(columns) {
      columns[0].x[0] = 7;
    },
  });
  assert.throws(() => world.step(), /not declared in write/);
  assert.equal(world.currentPhase, null);
});
