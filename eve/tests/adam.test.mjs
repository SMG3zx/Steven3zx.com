import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeAdam, resultState, startAdamFeed } from '../src/adam.mjs';
const rack = (loc = '6W101', serial = 'R1') => ({
  LOC: loc,
  RACK_SN: serial,
  TYPE: 'L11',
  MODEL: 'A5L',
  TIMESTAMP: '2026-08-20T13:38:00',
  UUTS: {
    '01': {
      TYPE: 'SERVER',
      SN: 'C1',
      RESULT: 'FAIL',
      STAGE: 'RUNIN',
      STATUS: 'Failure detail',
      RESULTS: { PRETEST: { RESULT: 'Y' } },
    },
    '02': { TYPE: 'SPACE', RESULT: null },
    '03': { TYPE: 'POWERSHELF', RESULT: null },
  },
});
const input = (data = { '6W101': rack() }, limited = false) => ({
  source: 'historical',
  locations: ['6W101', '6W102', '6V101'],
  snapshots: [
    {
      data,
      metadata: { limited, count: Object.keys(data).length, searchParameter: 'location:6W*' },
    },
  ],
});
test('preserves hierarchy, unknowns, empty slots and raw test fields', () => {
  const s = normalizeAdam(input()),
    a = s.assets.find((a) => a.id === 'R1');
  assert.equal(a.building, '6');
  assert.equal(a.pod, '6W');
  assert.equal(a.status, 'failed');
  assert.equal(a.summary.installed, 2);
  assert.equal(a.summary.empty, 1);
  assert.equal(a.summary.unknown, 1);
  assert.equal(a.components[0].status, 'Failure detail');
  assert.equal(s.timestamp, null);
  assert.equal(s.assets.find((a) => a.location === '6W102').status, 'empty');
  assert.equal(s.assets.find((a) => a.location === '6V101').status, 'unknown');
});
test('partial and count-mismatched responses preserve previous occupants', () => {
  const previous = normalizeAdam(input()).assets;
  assert.ok(normalizeAdam(input({}, true), previous).assets.some((a) => a.id === 'R1'));
  const mismatch = input({});
  mismatch.snapshots[0].metadata.count = 2;
  assert.ok(normalizeAdam(mismatch, previous).assets.some((a) => a.id === 'R1'));
  assert.ok(!normalizeAdam(input({}), previous).assets.some((a) => a.id === 'R1'));
});
test('rack moves retain serial identity; replacements supersede prior occupants', () => {
  const previous = normalizeAdam(input()).assets;
  const moved = normalizeAdam(input({ '6W102': rack('6W102') }, true), previous);
  assert.equal(moved.assets.filter((a) => a.id === 'R1').length, 1);
  assert.equal(moved.assets.find((a) => a.id === 'R1').location, '6W102');
  const replaced = normalizeAdam(input({ '6W101': rack('6W101', 'R2') }, true), previous);
  assert.ok(!replaced.assets.some((a) => a.id === 'R1'));
});
test('L10 stations work without inventing a rack serial', () => {
  const r = rack();
  delete r.RACK_SN;
  r.TYPE = 'L10';
  const a = normalizeAdam(input({ '6W101': r })).assets.find((a) => a.type === 'rack');
  assert.equal(a.id, 'station:6W101');
  assert.equal(a.serial, '');
});
test('invalid and out-of-order snapshots do not clear valid observations', () => {
  const previous = normalizeAdam(input()).assets;
  const old = rack();
  old.TIMESTAMP = '2026-01-01T00:00:00';
  assert.equal(
    normalizeAdam(input({ '6W101': old }), previous).assets.find((a) => a.id === 'R1').sourceTime,
    '2026-08-20T13:38:00',
  );
  assert.throws(() => normalizeAdam(input({ '6W101': rack('6V101') }), previous));
  assert.equal(previous.find((a) => a.id === 'R1').location, '6W101');
  assert.equal(resultState(null), 'unknown');
  assert.equal(resultState('F'), 'failed');
});
test('feed labels saved snapshots historical and retains state after a failure', async () => {
  const original = globalThis.fetch;
  let calls = 0,
    count = 0,
    stop;
  const states = [];
  globalThis.fetch = async () => {
    if (++calls > 1) throw new Error('unavailable');
    return { ok: true, json: async () => input() };
  };
  try {
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        stop?.();
        reject(new Error('timeout'));
      }, 1000);
      stop = startAdamFeed({
        interval: 1,
        onSnapshot: () => count++,
        onStatus: (s) => {
          states.push(s);
          if (s === 'offline') {
            stop();
            clearTimeout(timeout);
            resolve();
          }
        },
      });
    });
    assert.equal(count, 1);
    assert.deepEqual(states, ['historical', 'offline']);
  } finally {
    stop?.();
    globalThis.fetch = original;
  }
});
