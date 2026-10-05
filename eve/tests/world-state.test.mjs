import test from 'node:test';
import assert from 'node:assert/strict';
import { parseSnapshot, demoAssets, HUMAN_HEIGHT, startWorldFeed } from '../src/world-state.mjs';

const snapshot = () => ({
  timestamp: new Date().toISOString(),
  assets: demoAssets.map((a) => ({ ...a })),
});
test('accepts measured assets, including an empty complete snapshot', () => {
  assert.equal(parseSnapshot(snapshot()).assets.length, 24);
  assert.deepEqual(parseSnapshot({ ...snapshot(), assets: [] }).assets, []);
  assert.equal(HUMAN_HEIGHT, 1.8288);
});
test('rejects duplicate IDs, invalid geometry, invalid status and timestamps', () => {
  for (const change of [
    (a) => a.assets.push(a.assets[0]),
    (a) => (a.assets[0].height = -1),
    (a) => (a.assets[0].x = NaN),
    (a) => (a.assets[0].status = 'made-up'),
    (a) => (a.timestamp = 'invalid'),
    (a) => (a.assets[0].utilization = 101),
  ]) {
    const input = snapshot();
    change(input);
    assert.throws(() => parseSnapshot(input));
  }
});
test('demo feed is explicit and does not mutate its fixture', () => {
  let state;
  const stop = startWorldFeed({
    onSnapshot: (s) => (s.assets[0].x = 999),
    onStatus: (s) => (state = s),
  });
  assert.equal(state, 'demo');
  assert.equal(demoAssets[0].x, -12);
  stop();
});
test('live poll accepts updates and retains last snapshot on network failure', async () => {
  const original = globalThis.fetch;
  let calls = 0,
    count = 0,
    stop;
  globalThis.fetch = async () => {
    if (++calls > 1) throw new Error('Disconnected');
    return { ok: true, json: async () => snapshot() };
  };
  try {
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        stop?.();
        reject(new Error('Feed test timed out'));
      }, 1000);
      stop = startWorldFeed({
        url: '/api/world',
        interval: 1,
        onSnapshot: () => count++,
        onStatus: (state) => {
          if (state === 'offline') {
            stop();
            clearTimeout(timeout);
            resolve();
          }
        },
      });
    });
    assert.equal(count, 1);
  } finally {
    stop?.();
    globalThis.fetch = original;
  }
});
