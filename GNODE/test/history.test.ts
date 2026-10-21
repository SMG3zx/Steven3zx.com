import { describe, expect, test } from "bun:test";
import { History } from "../src/web/history";

function clock() {
  let now = 0;
  return { now: () => now, tick: (ms: number) => (now += ms) };
}

describe("History", () => {
  test("undo returns the state from before the last change, and redo goes forward again", () => {
    const h = new History<string>();
    h.record("a");
    expect(h.undo("b")).toBe("a");
    expect(h.canUndo).toBe(false);
    expect(h.redo("a")).toBe("b");
    expect(h.canRedo).toBe(false);
  });

  test("nothing to undo or redo returns undefined", () => {
    const h = new History<string>();
    expect(h.undo("a")).toBeUndefined();
    expect(h.redo("a")).toBeUndefined();
  });

  test("a new change clears the redo stack", () => {
    const h = new History<string>();
    h.record("a");
    h.undo("b");
    expect(h.canRedo).toBe(true);
    h.record("a");
    expect(h.canRedo).toBe(false);
  });

  test("edits with the same key within the window merge into one step", () => {
    const c = clock();
    const h = new History<string>(200, 1000, c.now);
    h.record("start", "param:n1:text");
    c.tick(300);
    h.record("s", "param:n1:text");
    c.tick(300);
    h.record("st", "param:n1:text");
    expect(h.undo("star")).toBe("start");
    expect(h.canUndo).toBe(false);
  });

  test("edits with the same key separated by more than the window are separate steps", () => {
    const c = clock();
    const h = new History<string>(200, 1000, c.now);
    h.record("one", "k");
    c.tick(1500);
    h.record("two", "k");
    expect(h.undo("three")).toBe("two");
    expect(h.undo("two")).toBe("one");
  });

  test("a different key or an unkeyed change starts a new step", () => {
    const c = clock();
    const h = new History<string>(200, 1000, c.now);
    h.record("a", "x");
    h.record("b", "y");
    h.record("c");
    h.record("d", "y");
    expect(h.undo("e")).toBe("d");
    expect(h.undo("d")).toBe("c");
    expect(h.undo("c")).toBe("b");
    expect(h.undo("b")).toBe("a");
  });

  test("history is capped at the limit, dropping the oldest steps", () => {
    const h = new History<number>(2);
    h.record(1);
    h.record(2);
    h.record(3);
    expect(h.undo(4)).toBe(3);
    expect(h.undo(3)).toBe(2);
    expect(h.undo(2)).toBeUndefined();
  });

  test("clear forgets both stacks", () => {
    const h = new History<string>();
    h.record("a");
    h.undo("b");
    h.clear();
    expect(h.canUndo).toBe(false);
    expect(h.canRedo).toBe(false);
  });
});
