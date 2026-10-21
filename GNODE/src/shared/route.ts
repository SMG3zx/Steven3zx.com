// Wire geometry, kept free of the DOM so it can be tested. Points are canvas
// pixels. A wire leaves its output port to the right and enters its input port
// from the left, so both ends run horizontally before they turn.

export interface Pt {
  x: number;
  y: number;
}

// How far a square wire runs straight out of an output port and into an input
// port before it turns. JOG is the least gap kept between a wire and a port row.
export const STUB = 24;
export const JOG = 24;
// The radius of the rounded corners on square wires.
export const RADIUS = 12;

export function dist(a: Pt, b: Pt): number {
  return Math.hypot(b.x - a.x, b.y - a.y);
}

// Drops a point that repeats the one before it, and a middle point that lies on
// the straight line through its neighbours, so the corners are the real ones.
export function tidy(pts: Pt[]): Pt[] {
  const out: Pt[] = [];
  for (const p of pts) {
    const last = out[out.length - 1];
    if (last && dist(last, p) < 0.5) continue;
    const prev = out[out.length - 2];
    if (prev && last && (sameX(prev, last) && sameX(last, p) || sameY(prev, last) && sameY(last, p))) {
      out[out.length - 1] = p;
      continue;
    }
    out.push(p);
  }
  return out;
}

function sameX(a: Pt, b: Pt): boolean {
  return Math.abs(a.x - b.x) < 0.5;
}

function sameY(a: Pt, b: Pt): boolean {
  return Math.abs(a.y - b.y) < 0.5;
}

// The waypoints of a square wire from out to inn. With no bends it runs out,
// across and in. With bends it turns at each bend point, in order.
export function orthoPoints(out: Pt, inn: Pt, bends: Pt[] = []): Pt[] {
  const s0 = { x: out.x + STUB, y: out.y };
  const e0 = { x: inn.x - STUB, y: inn.y };

  if (bends.length > 0) {
    const pts: Pt[] = [out, s0];
    let cur = s0;
    for (const b of [...bends, e0]) {
      pts.push({ x: b.x, y: cur.y }, b);
      cur = b;
    }
    pts.push(inn);
    return tidy(pts);
  }

  if (e0.x >= s0.x) {
    const mx = (s0.x + e0.x) / 2;
    return tidy([out, s0, { x: mx, y: s0.y }, { x: mx, y: e0.y }, e0, inn]);
  }

  // The input sits left of the output: the wire goes round below both ports.
  const low = Math.abs(e0.y - s0.y) < JOG * 2 ? Math.max(s0.y, e0.y) + JOG * 2 : (s0.y + e0.y) / 2;
  return tidy([out, s0, { x: s0.x, y: low }, { x: e0.x, y: low }, e0, inn]);
}

function towards(from: Pt, to: Pt, r: number): Pt {
  const len = dist(from, to) || 1;
  return { x: from.x + ((to.x - from.x) / len) * r, y: from.y + ((to.y - from.y) / len) * r };
}

// An SVG path through the points, with each interior corner rounded. A corner
// is never rounded by more than half of either segment next to it.
export function pathD(pts: Pt[], radius = RADIUS): string {
  if (pts.length === 0) return "";
  let d = `M ${pts[0].x} ${pts[0].y}`;
  for (let i = 1; i < pts.length - 1; i++) {
    const prev = pts[i - 1], c = pts[i], next = pts[i + 1];
    const r = Math.min(radius, dist(prev, c) / 2, dist(c, next) / 2);
    if (r <= 0.5) {
      d += ` L ${c.x} ${c.y}`;
      continue;
    }
    const a = towards(c, prev, r), b = towards(c, next, r);
    d += ` L ${a.x} ${a.y} Q ${c.x} ${c.y} ${b.x} ${b.y}`;
  }
  if (pts.length > 1) d += ` L ${pts[pts.length - 1].x} ${pts[pts.length - 1].y}`;
  return d;
}

// The curved wire used when square wires are off.
export function curvePath(out: Pt, inn: Pt): string {
  const reach = Math.max(60, Math.abs(inn.x - out.x) * 0.5);
  return `M ${out.x} ${out.y} C ${out.x + reach} ${out.y}, ${inn.x - reach} ${inn.y}, ${inn.x} ${inn.y}`;
}

// The SVG path for a wire, square or curved.
export function wireD(out: Pt, inn: Pt, bends: Pt[], square: boolean): string {
  return square ? pathD(orthoPoints(out, inn, bends)) : curvePath(out, inn);
}

export function distToSegment(p: Pt, a: Pt, b: Pt): number {
  const dx = b.x - a.x, dy = b.y - a.y;
  const len2 = dx * dx + dy * dy;
  const t = len2 === 0 ? 0 : Math.max(0, Math.min(1, ((p.x - a.x) * dx + (p.y - a.y) * dy) / len2));
  return Math.hypot(a.x + t * dx - p.x, a.y + t * dy - p.y);
}

// Where a new bend goes if the wire is grabbed at p: the index in bends of the
// leg of the wire nearest p.
export function bendSlot(out: Pt, inn: Pt, bends: Pt[], p: Pt): number {
  const pts = [out, ...bends, inn];
  let best = 0;
  let bestDist = Infinity;
  for (let i = 0; i < pts.length - 1; i++) {
    const d = distToSegment(p, pts[i], pts[i + 1]);
    if (d < bestDist) {
      bestDist = d;
      best = i;
    }
  }
  return best;
}
