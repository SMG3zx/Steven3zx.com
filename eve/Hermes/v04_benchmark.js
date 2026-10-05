'use strict';

const { Hermes } = require('./Hermes.js');
const { spawnSync } = require('node:child_process');

const CASE = process.argv[2] ?? 'all';
const COUNT = positiveInteger(process.argv[3], 100_000);
const RUNS = positiveInteger(process.argv[4], 7);
const DT = 1 / 60;

function positiveInteger(value, fallback) {
  if (value === undefined) return fallback;
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed <= 0)
    throw new Error(`expected a positive integer, received ${String(value)}`);
  return parsed;
}

function time(operation) {
  const started = process.hrtime.bigint();
  const units = operation();
  return { ms: Number(process.hrtime.bigint() - started) / 1e6, units };
}

function summarize(name, samples) {
  const sorted = samples.map((sample) => sample.ms).sort((a, b) => a - b);
  const pick = (percentile) =>
    sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * percentile))];
  const median = pick(0.5),
    units = samples[0].units;
  return {
    name,
    count: units,
    samples: samples.length,
    medianMs: median,
    p95Ms: pick(0.95),
    p99Ms: pick(0.99),
    operationsPerSecond: median ? (units * 1000) / median : 0,
    nsPerOperation: units ? (median * 1e6) / units : 0,
    frameBudgetPercent: (median / 16.666666667) * 100,
    arrayBuffersMiB: process.memoryUsage().arrayBuffers / 1024 / 1024,
    rssMiB: process.memoryUsage().rss / 1024 / 1024,
  };
}

function fixture(count, options = {}) {
  const world = new Hermes.World({
    entityCapacity: count + 1024,
    chunkBytes: 256 * 1024,
    ...options,
  });
  const Position = world.component('Position', {
    x: Float32Array,
    y: Float32Array,
    z: Float32Array,
  });
  const Temperature = world.component('Temperature', { value: Float32Array });
  const Alarm = world.component('Alarm', { severity: Uint8Array });
  const base = world.archetype(Position, Temperature);
  return { world, Position, Temperature, Alarm, base };
}

function bulkCommands() {
  const samples = [];
  const world = new Hermes.World({ entityCapacity: 1 });
  const commands = world.command('Telemetry', { entity: Uint32Array, value: Float32Array }, COUNT);
  for (let run = 0; run < RUNS; run++) {
    commands.clear();
    samples.push(
      time(() => {
        for (let i = 0; i < COUNT; i++) commands.push({ entity: i, value: i });
        return COUNT;
      }),
    );
  }
  return summarize('bulk-commands', samples);
}

function snapshotIngestion() {
  const samples = [];
  for (let run = 0; run < RUNS; run++) {
    const { world, base } = fixture(COUNT, { schemaVersion: 4 });
    const entities = Array.from({ length: COUNT }, (_, id) => ({
      id,
      version: 1,
      components: { Position: { x: id }, Temperature: { value: 20 } },
    }));
    samples.push(
      time(() => {
        world.ingestSnapshot({ schemaVersion: 4, entities }, { resolveArchetype: () => base });
        return COUNT;
      }),
    );
    if (global.gc) global.gc();
  }
  return summarize('snapshot-ingestion', samples);
}

function partialUpdates() {
  const { world, Temperature, base } = fixture(COUNT, { schemaVersion: 4 });
  const rows = Array.from({ length: COUNT }, (_, id) => ({
    Position: { x: id },
    Temperature: { value: 20 },
  }));
  const ids = Array.from({ length: COUNT }, (_, id) => id);
  world.spawnMany(base, rows, ids);
  const samples = [];
  for (let run = 0; run < RUNS; run++) {
    const updates = ids.map((id) => ({
      id,
      component: Temperature,
      version: run + 1,
      values: { value: 20 + run },
    }));
    samples.push(
      time(() => {
        world.applyUpdates(updates);
        return COUNT;
      }),
    );
  }
  return summarize('partial-updates', samples);
}

function changeExtraction() {
  const { world, Temperature, base } = fixture(COUNT);
  const rows = Array.from({ length: COUNT }, (_, id) => ({
    Position: { x: id },
    Temperature: { value: 20 },
  }));
  const handles = world.spawnMany(base, rows);
  world.step(DT);
  const query = world.compileQuery(Temperature),
    samples = [];
  for (let run = 0; run < RUNS; run++) {
    world.updateMany(handles, Temperature, { value: 21 + run });
    const frame = world.step(DT);
    samples.push(
      time(() => {
        world.filterChanges(frame.changes, { query, components: [Temperature] });
        return COUNT;
      }),
    );
  }
  return summarize('change-extraction', samples);
}

function checksumCost() {
  const { world, base } = fixture(COUNT, { checksum: true });
  const rows = Array.from({ length: COUNT }, (_, id) => ({
    Position: { x: id },
    Temperature: { value: 20 },
  }));
  world.spawnMany(base, rows);
  const samples = [];
  for (let run = 0; run < RUNS; run++)
    samples.push(
      time(() => {
        world.checksum();
        return COUNT;
      }),
    );
  return summarize('checksum', samples);
}

function replayCost() {
  const ids = Array.from({ length: COUNT }, (_, id) => id),
    rows = ids.map((id) => ({ Position: { x: id }, Temperature: { value: 20 } }));
  const apply = (payload, world) =>
    world.applyUpdates(
      ids.map((id) => ({
        id,
        component: 'Temperature',
        version: payload.version,
        values: { value: payload.value },
      })),
    );
  const source = fixture(COUNT, { checksum: true });
  source.world.spawnMany(source.base, rows, ids);
  source.world.startRecording();
  source.world.input('telemetry', { version: 1, value: 21 }, apply);
  source.world.step(DT);
  const log = source.world.stopRecording(),
    samples = [];
  for (let run = 0; run < RUNS; run++) {
    const target = fixture(COUNT, { checksum: true });
    target.world.spawnMany(target.base, rows, ids);
    samples.push(
      time(() => {
        target.world.replay(log, { telemetry: apply }, DT);
        return COUNT;
      }),
    );
    if (global.gc) global.gc();
  }
  return summarize('replay', samples);
}

function structuralCommit() {
  const samples = [];
  for (let run = 0; run < RUNS; run++) {
    const { world, Alarm, base } = fixture(COUNT);
    const rows = Array.from({ length: COUNT }, (_, id) => ({
      Position: { x: id },
      Temperature: { value: 20 },
    }));
    const handles = world.spawnMany(base, rows);
    world.addMany(
      handles,
      Alarm,
      handles.map(() => ({ severity: 1 })),
    );
    samples.push(
      time(() => {
        world.step(DT);
        return COUNT;
      }),
    );
    if (global.gc) global.gc();
  }
  return summarize('structural-commit', samples);
}

const CASES = {
  'bulk-commands': bulkCommands,
  'snapshot-ingestion': snapshotIngestion,
  'partial-updates': partialUpdates,
  'change-extraction': changeExtraction,
  checksum: checksumCost,
  replay: replayCost,
  'structural-commit': structuralCommit,
};

if (CASE === 'all') {
  const results = Object.keys(CASES).map((name) => {
    const child = spawnSync(
      process.execPath,
      [...process.execArgv, __filename, name, String(COUNT), String(RUNS)],
      { encoding: 'utf8' },
    );
    if (child.status !== 0) throw new Error(child.stderr || `benchmark ${name} failed`);
    return JSON.parse(child.stdout);
  });
  console.log(JSON.stringify(results, null, 2));
} else {
  if (!CASES[CASE]) throw new Error(`unknown benchmark case ${CASE}`);
  console.log(JSON.stringify(CASES[CASE](), null, 2));
}
