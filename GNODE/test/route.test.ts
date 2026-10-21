import { describe, expect, test } from "bun:test";
import { STUB, bendSlot, orthoPoints, pathD, tidy, wireD } from "../src/shared/route";

describe("orthoPoints", () => {
  test("a wire that runs forward turns once, halfway between the stubs", () => {
    const pts = orthoPoints({ x: 0, y: 0 }, { x: 200, y: 100 });
    // The stub points lie on the straight runs in and out, so only the corners remain.
    expect(pts).toEqual([
      { x: 0, y: 0 },
      { x: 100, y: 0 },
      { x: 100, y: 100 },
      { x: 200, y: 100 },
    ]);
    expect(STUB).toBeGreaterThan(0);
  });

  test("every leg is horizontal or vertical", () => {
    const cases = [
      orthoPoints({ x: 0, y: 0 }, { x: 200, y: 100 }),
      orthoPoints({ x: 300, y: 0 }, { x: 40, y: 200 }),
      orthoPoints({ x: 0, y: 50 }, { x: 400, y: 50 }),
      orthoPoints({ x: 0, y: 0 }, { x: 200, y: 100 }, [{ x: 150, y: 300 }]),
    ];
    for (const pts of cases) {
      for (let i = 1; i < pts.length; i++) {
        const sameX = Math.abs(pts[i].x - pts[i - 1].x) < 0.5;
        const sameY = Math.abs(pts[i].y - pts[i - 1].y) < 0.5;
        expect(sameX || sameY).toBe(true);
      }
    }
  });

  test("a wire into a block on its left goes round below both ports", () => {
    const pts = orthoPoints({ x: 300, y: 0 }, { x: 40, y: 20 });
    const lowest = Math.max(...pts.map((p) => p.y));
    expect(lowest).toBeGreaterThan(20);
    expect(pts[pts.length - 1]).toEqual({ x: 40, y: 20 });
  });

  test("a bend is a corner the wire passes through", () => {
    const bend = { x: 150, y: 300 };
    const pts = orthoPoints({ x: 0, y: 0 }, { x: 200, y: 100 }, [bend]);
    expect(pts).toContainEqual(bend);
  });
});

describe("tidy", () => {
  test("drops repeats and points that lie on a straight line", () => {
    const pts = tidy([
      { x: 0, y: 0 },
      { x: 0, y: 0 },
      { x: 10, y: 0 },
      { x: 20, y: 0 },
      { x: 20, y: 10 },
    ]);
    expect(pts).toEqual([
      { x: 0, y: 0 },
      { x: 20, y: 0 },
      { x: 20, y: 10 },
    ]);
  });
});

describe("pathD", () => {
  test("starts at the first point, ends at the last, and rounds each corner", () => {
    const d = pathD([
      { x: 0, y: 0 },
      { x: 100, y: 0 },
      { x: 100, y: 100 },
    ]);
    expect(d.startsWith("M 0 0")).toBe(true);
    expect(d.endsWith("L 100 100")).toBe(true);
    expect(d).toContain("Q 100 0");
  });

  test("a short leg is never rounded past its middle", () => {
    const d = pathD([
      { x: 0, y: 0 },
      { x: 4, y: 0 },
      { x: 4, y: 100 },
    ]);
    // The corner at (4, 0) can only cut back 2 px along the 4 px leg.
    expect(d).toContain("L 2 0 Q 4 0 4 2");
  });
});

describe("wireD", () => {
  test("a curved wire is a cubic curve", () => {
    expect(wireD({ x: 0, y: 0 }, { x: 200, y: 100 }, [], false)).toContain(" C ");
  });

  test("a square wire is made of straight and rounded legs", () => {
    expect(wireD({ x: 0, y: 0 }, { x: 200, y: 100 }, [], true)).toContain(" Q ");
  });
});

describe("bendSlot", () => {
  test("a grab near the first leg makes the first bend", () => {
    const out = { x: 0, y: 0 }, inn = { x: 300, y: 300 };
    const bends = [{ x: 100, y: 200 }];
    expect(bendSlot(out, inn, bends, { x: 150, y: 2 })).toBe(0);
  });

  test("a grab near the last leg makes the last bend", () => {
    const out = { x: 0, y: 0 }, inn = { x: 300, y: 300 };
    const bends = [{ x: 100, y: 200 }];
    expect(bendSlot(out, inn, bends, { x: 250, y: 300 })).toBe(1);
  });
});
