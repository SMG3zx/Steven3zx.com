export const HUMAN_HEIGHT = 1.8288;
export const demoAssets = Array.from({ length: 24 }, (_, i) => ({
  id: `${i < 12 ? 'V' : 'B'}-${String((i % 12) + 1).padStart(2, '0')}`,
  name: `${i < 12 ? 'V Pod' : 'Storage'} ${String((i % 12) + 1).padStart(2, '0')}`,
  type: i < 12 ? 'pod' : 'storage',
  x: -12 + (i % 6) * 4.8,
  z: -8 + Math.floor(i / 6) * 5.4,
  width: 2.4,
  height: i < 12 ? 2.4 : 3.2,
  depth: 1.5,
  status: i === 3 ? 'warning' : 'healthy',
  utilization: 30 + ((i * 17) % 65),
}));

// Full snapshots are validated atomically; invalid updates never erase the last good world.
export function parseSnapshot(input) {
  if (!input || !Array.isArray(input.assets) || input.assets.length > 2000)
    throw new Error('Expected an assets array (maximum 2000).');
  const timestamp = Date.parse(input.timestamp);
  if (!Number.isFinite(timestamp) || timestamp > Date.now() + 60000)
    throw new Error('Invalid snapshot timestamp.');
  const ids = new Set();
  const assets = input.assets.map((a) => {
    if (!a || typeof a.id !== 'string' || !a.id || a.id.length > 80 || ids.has(a.id))
      throw new Error('Asset IDs must be unique strings.');
    ids.add(a.id);
    if (
      !['pod', 'storage'].includes(a.type) ||
      !['healthy', 'warning', 'critical', 'offline'].includes(a.status)
    )
      throw new Error('Invalid asset type or status.');
    if (![a.x, a.z].every((v) => Number.isFinite(v) && Math.abs(v) <= 500))
      throw new Error('Invalid position.');
    if (![a.width, a.height, a.depth].every((v) => Number.isFinite(v) && v > 0 && v <= 50))
      throw new Error('Invalid dimensions.');
    if (!Number.isFinite(a.utilization) || a.utilization < 0 || a.utilization > 100)
      throw new Error('Invalid utilization.');
    return {
      id: a.id,
      name: String(a.name || a.id).slice(0, 100),
      type: a.type,
      status: a.status,
      x: a.x,
      z: a.z,
      width: a.width,
      height: a.height,
      depth: a.depth,
      utilization: a.utilization,
    };
  });
  return { assets, timestamp };
}

export function startWorldFeed({ url, onSnapshot, onStatus, interval = 5000 }) {
  let stopped = false,
    timer,
    controller,
    lastTimestamp = -Infinity;
  if (!url) {
    onSnapshot({ assets: demoAssets.map((a) => ({ ...a })), timestamp: Date.now() });
    onStatus('demo', 'Sample layout · dimensions unverified');
    return () => {};
  }
  async function poll() {
    controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 8000);
    try {
      const response = await fetch(url, {
        signal: controller.signal,
        cache: 'no-store',
        credentials: 'same-origin',
      });
      if (!response.ok) throw new Error(`Feed returned ${response.status}`);
      const snapshot = parseSnapshot(await response.json());
      if (snapshot.timestamp < lastTimestamp) throw new Error('Out-of-order snapshot');
      if (!stopped) {
        lastTimestamp = snapshot.timestamp;
        onSnapshot(snapshot);
        onStatus(
          Date.now() - snapshot.timestamp > 15000 ? 'stale' : 'live',
          'Connected to warehouse feed',
        );
      }
    } catch (error) {
      if (!stopped) onStatus('offline', `${error.message} · retaining last known state`);
    } finally {
      clearTimeout(timeout);
      if (!stopped) timer = setTimeout(poll, interval);
    }
  }
  onStatus('connecting', 'Waiting for warehouse data');
  poll();
  return () => {
    stopped = true;
    clearTimeout(timer);
    controller?.abort();
  };
}
