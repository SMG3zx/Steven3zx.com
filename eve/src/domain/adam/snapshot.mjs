import { BASE_POD, BASE_RACK, createPodBase } from '../warehouse/base.mjs';

export const HUMAN_HEIGHT = 1.8288;
export const STALE_AFTER_MS = 300000;
const text = (v) => (v == null ? '' : String(v).trim());
const measured = (...values) => values.map(Number).find((v) => Number.isFinite(v) && v > 0) || null;
const locationParts = (value) =>
  /^(\d+[A-Z])((?:\d{2,3})|(?:[A-Z]\d{2}))$/.exec(String(value || ''));
const validLocation = (value) => Boolean(locationParts(value));
const podOf = (value) => locationParts(value)?.[1] || '';
export function resultState(result) {
  const r = text(result).toUpperCase();
  return ['FAIL', 'N', 'F'].includes(r)
    ? 'failed'
    : r === 'PASS' || r === 'Y'
      ? 'passed'
      : ['START', 'RUNNING'].includes(r)
        ? 'running'
        : 'unknown';
}
export function cableFindings(value) {
  const raw = text(value);
  if (!raw) return [];
  const upper = raw.toUpperCase();
  if (!/(CABLE|LOOPBACK|QSFP|BMC IP|K2 IP|LEAK)/.test(upper)) return [];
  const type =
    upper.includes('CX8') || upper.includes('LOOPBACK')
      ? 'CX8 loopback'
      : upper.includes('BMC')
        ? 'BMC service lead'
        : upper.includes('K2')
          ? 'K2 network lead'
          : upper.includes('LEAK')
            ? 'Leak-cable check'
            : 'Cable / connector check';
  const side = upper.includes('BOTH')
    ? 'both'
    : upper.includes('LEFT')
      ? 'left'
      : upper.includes('RIGHT')
        ? 'right'
        : 'unknown';
  const state = /FAIL|REPLACE|UNREACHABLE|ISSUE|NOT PRESENT/.test(upper)
    ? 'attention'
    : /PASS|ACTIVE|SUCCESS/.test(upper)
      ? 'passed'
      : 'observed';
  return [{ type, side, state, note: raw.slice(0, 320) }];
}
export function summarize(components) {
  const counts = { installed: 0, empty: 0, failed: 0, passed: 0, running: 0, unknown: 0 };
  for (const c of components) {
    if (c.type === 'SPACE') counts.empty++;
    else {
      counts.installed++;
      counts[c.state]++;
    }
  }
  return {
    ...counts,
    state: counts.failed
      ? 'failed'
      : counts.running
        ? 'running'
        : counts.unknown
          ? 'unknown'
          : counts.passed
            ? 'passed'
            : 'unknown',
  };
}
// Timestamps without an offset are retained verbatim, never interpreted in the viewer's timezone.
export function zonedTime(value) {
  return typeof value === 'string' &&
    /(?:Z|[+-]\d\d:\d\d)$/.test(value) &&
    Number.isFinite(Date.parse(value))
    ? Date.parse(value)
    : null;
}
export function normalizeAdam(input, previous = []) {
  if (!input || !Array.isArray(input.snapshots) || !Array.isArray(input.locations))
    throw new Error('Expected ADAM snapshots and configured locations.');
  const layout = new Set(input.locations);
  if ([...layout].some((loc) => !validLocation(loc)))
    throw new Error('Invalid configured location.');
  const racks = new Map(previous.filter((a) => a.type === 'rack').map((a) => [a.id, a]));
  let partial = false;
  const seen = new Set();
  const completePods = new Set();
  for (const snapshot of input.snapshots) {
    if (!snapshot?.data || Array.isArray(snapshot.data) || typeof snapshot.data !== 'object')
      throw new Error('Invalid ADAM data.');
    const scope = /^location:(\d+[A-Z]+)\*$/.exec(snapshot.metadata?.searchParameter || '')?.[1];
    const complete = Boolean(
      scope &&
      snapshot.metadata?.limited === false &&
      snapshot.metadata?.count === Object.keys(snapshot.data).length,
    );
    partial ||= !complete;
    const oldTimes = previous
      .filter((a) => a.pod === scope && a.sourceTime)
      .map((a) => a.sourceTime)
      .sort();
    const newTimes = Object.values(snapshot.data)
      .map((a) => text(a?.TIMESTAMP))
      .filter(Boolean)
      .sort();
    if (oldTimes.length && newTimes.length && newTimes.at(-1) < oldTimes.at(-1)) {
      partial = true;
      continue;
    }
    if (complete) completePods.add(scope);
    if (complete) for (const [id, a] of racks) if (a.pod === scope) racks.delete(id);
    for (const [key, r] of Object.entries(snapshot.data)) {
      if (
        !r ||
        !validLocation(r.LOC) ||
        key !== r.LOC ||
        (!text(r.RACK_SN) && r.TYPE !== 'L10') ||
        !r.UUTS ||
        typeof r.UUTS !== 'object' ||
        Array.isArray(r.UUTS)
      )
        throw new Error('Invalid rack identity or UUTS.');
      const pod = podOf(r.LOC),
        id = text(r.RACK_SN) || `station:${r.LOC}`;
      if (scope && pod !== scope) throw new Error('Rack is outside the declared pod scope.');
      if (seen.has(id)) throw new Error('Duplicate rack serial in response.');
      seen.add(id);
      layout.add(r.LOC);
      const components = Object.entries(r.UUTS).map(([slot, c]) => {
        if (!c || typeof c !== 'object' || Array.isArray(c)) throw new Error('Invalid component.');
        const results = Object.entries(c.RESULTS || {}).map(([stage, v]) => ({
          stage,
          result: text(v?.RESULT),
          status: text(v?.STATUS),
          station: text(v?.STATION),
          time: text(v?.DATE_TIME),
        }));
        const cables = [
          c.STATUS,
          ...results.map((v) => v.status),
          ...results.map((v) => v.result),
        ].flatMap(cableFindings);
        return {
          slot,
          location: text(c.LOC) || slot,
          serial: text(c.SN),
          type: text(c.TYPE) || 'UNKNOWN',
          part: text(c.COMP_PN || c.PN),
          status: text(c.STATUS),
          result: text(c.RESULT),
          stage: text(c.STAGE),
          station: text(c.STATION),
          eventTime: text(c.STATUS_DATE_TIME),
          state: resultState(c.RESULT),
          cables,
          results,
        };
      });
      // An observed replacement at the same location supersedes the previous occupant even in partial responses.
      for (const [oldId, old] of racks)
        if (old.location === r.LOC && oldId !== id) racks.delete(oldId);
      racks.set(id, {
        id,
        kind: text(r.TYPE),
        serial: text(r.RACK_SN),
        type: 'rack',
        name: r.LOC,
        location: r.LOC,
        pod,
        building: pod.slice(0, -1),
        model: text(r.MODEL),
        subModel: text(r.SUB_MODEL),
        part: text(r.RACK_PN),
        width: measured(r.WIDTH, r.DIMENSIONS?.WIDTH, r.GEOMETRY?.WIDTH),
        height: measured(r.HEIGHT, r.DIMENSIONS?.HEIGHT, r.GEOMETRY?.HEIGHT),
        depth: measured(r.DEPTH, r.DIMENSIONS?.DEPTH, r.GEOMETRY?.DEPTH),
        checkin: `${text(r.CHECKIN_DATE)} ${text(r.CHECKIN_TIME)}`.trim(),
        sourceTime: text(r.TIMESTAMP),
        timestamp: zonedTime(r.TIMESTAMP),
        components,
        cables: components.flatMap((c) => c.cables || []),
        summary: summarize(components),
      });
    }
  }
  for (const a of racks.values()) layout.add(a.location);
  const pods = [...new Set([...layout].map(podOf))].filter(Boolean).sort();
  const podBases = Object.fromEntries(
    pods.map((pod) => [
      pod,
      createPodBase(
        pod,
        [...layout].filter((loc) => podOf(loc) === pod),
      ),
    ]),
  );
  const positions = new Map();
  pods.forEach((pod, p) => {
    const locations = [...layout].filter((loc) => podOf(loc) === pod).sort();
    // Pods run side-by-side in parallel horizontal lanes. Their rack rows share
    // the same X direction and are offset along Z to leave a pod aisle.
    const centerX = 0;
    // Keep every pod parallel while reserving a human-scale service aisle
    // between its two rack rows. Spacing is derived from the base rack/pod
    // dimensions instead of a fixed pixel-like offset.
    const rowSpacing = BASE_RACK.width + 0.65;
    const podSpacing = Math.max(8.5, BASE_POD.aisleWidth + BASE_RACK.depth + 2.2);
    const centerZ = (p - (pods.length - 1) / 2) * podSpacing;
    const sideCount = Math.ceil(locations.length / 2);
    // ADAM location numbering is retained while alternating locations across the two pod sides.
    // The component face points outward from the pod centerline, leaving a usable central aisle.
    locations.forEach((loc, i) => {
      const side =
        Number(String(locationParts(loc)?.[2] || '').replace(/\D/g, '')) % 2 === 0 ? 1 : -1;
      const rowIndex = Math.floor(i / 2);
      positions.set(loc, {
        x: centerX + (rowIndex - (sideCount - 1) / 2) * rowSpacing,
        z: centerZ + side * (BASE_POD.aisleWidth / 2 + BASE_RACK.depth / 2),
        podCenterZ: centerZ,
        rowSide: side,
        facing: side > 0 ? 0 : Math.PI,
      });
    });
  });
  const assets = [...racks.values()].map((a) => ({
    ...a,
    ...positions.get(a.location),
    width: a.width || BASE_RACK.width,
    height: a.height || BASE_RACK.height,
    depth: a.depth || BASE_RACK.depth,
    base: BASE_RACK,
    podBase: podBases[a.pod],
    status: a.summary.state,
  }));
  const occupied = new Set(assets.map((a) => a.location));
  for (const loc of layout)
    if (!occupied.has(loc))
      assets.push({
        id: `location:${loc}`,
        type: 'rack',
        baseOnly: true,
        kind: 'BASE',
        name: loc,
        location: loc,
        pod: podOf(loc),
        building: podOf(loc).replace(/^\d+/, ''),
        ...positions.get(loc),
        width: BASE_RACK.width,
        height: BASE_RACK.height,
        depth: BASE_RACK.depth,
        base: BASE_RACK,
        podBase: podBases[podOf(loc)],
        status: completePods.has(podOf(loc)) ? 'empty' : 'unknown',
        components: [],
        summary: { installed: 0, empty: 0 },
        sourceTime: '',
      });
  const observed = assets.filter((a) => a.type === 'rack');
  return {
    assets,
    pods,
    podBases,
    partial,
    historical: input.source === 'historical',
    receivedAt: Date.now(),
    timestamp:
      observed.length && observed.every((a) => a.timestamp != null)
        ? Math.min(...observed.map((a) => a.timestamp))
        : null,
  };
}
export function startAdamFeed({
  url = '/api/adam-snapshot',
  onSnapshot,
  onStatus,
  interval = 120000,
}) {
  let stopped = false,
    timer,
    controller,
    previous = [];
  async function poll() {
    controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 10000);
    try {
      const response = await fetch(url, {
        cache: 'no-store',
        signal: controller.signal,
        credentials: 'same-origin',
      });
      if (!response.ok) throw new Error(`ADAM feed returned ${response.status}`);
      const next = normalizeAdam(await response.json(), previous);
      if (!stopped) {
        previous = next.assets;
        onSnapshot(next);
        onStatus(
          next.historical
            ? 'historical'
            : next.partial
              ? 'partial'
              : next.timestamp == null
                ? 'unknown'
                : Date.now() - next.timestamp > STALE_AFTER_MS
                  ? 'stale'
                  : 'live',
          next.historical ? 'Saved ADAM snapshot · not live telemetry' : 'ADAM observations',
        );
      }
    } catch (error) {
      if (!stopped) onStatus('offline', `${error.message} · last known observations retained`);
    } finally {
      clearTimeout(timeout);
      if (!stopped) timer = setTimeout(poll, interval);
    }
  }
  poll();
  return () => {
    stopped = true;
    clearTimeout(timer);
    controller?.abort();
  };
}
