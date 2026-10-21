import { describe, expect, test } from "bun:test";
import { copyFragment, emptyGraph, pasteFragment, type Graph } from "../src/shared/graph";

const sample: Graph = {
  ...emptyGraph("sample"),
  nodes: [
    { id: "a", kind: "main", x: 10, y: 10, params: {} },
    { id: "b", kind: "print", x: 200, y: 10, params: {} },
    { id: "c", kind: "literal", x: 200, y: 200, params: { type: "int", value: "1" } },
  ],
  wires: [
    { id: "w1", from: { node: "a", port: "body" }, to: { node: "b", port: "in" } },
    { id: "w2", from: { node: "c", port: "value" }, to: { node: "b", port: "value" } },
  ],
};

describe("copyFragment", () => {
  test("keeps the picked blocks and only the wires between them", () => {
    const frag = copyFragment(sample, ["a", "b"]);
    expect(frag.nodes.map((n) => n.id)).toEqual(["a", "b"]);
    expect(frag.wires.map((w) => w.id)).toEqual(["w1"]);
  });

  test("the copy does not share objects with the graph", () => {
    const frag = copyFragment(sample, ["a"]);
    frag.nodes[0].x = 999;
    expect(sample.nodes[0].x).toBe(10);
  });
});

describe("pasteFragment", () => {
  test("gives new ids and moves the blocks by the offset", () => {
    const pasted = pasteFragment(copyFragment(sample, ["a", "b"]), 40, 40);
    const oldIds = new Set(sample.nodes.map((n) => n.id));
    for (const n of pasted.nodes) expect(oldIds.has(n.id)).toBe(false);
    expect(pasted.nodes.map((n) => [n.x, n.y])).toEqual([
      [50, 50],
      [240, 50],
    ]);
  });

  test("re-points the wires at the new blocks", () => {
    const pasted = pasteFragment(copyFragment(sample, ["a", "b"]), 40, 40);
    const [newA, newB] = pasted.nodes.map((n) => n.id);
    expect(pasted.wires).toHaveLength(1);
    expect(pasted.wires[0].from.node).toBe(newA);
    expect(pasted.wires[0].to.node).toBe(newB);
  });

  test("pasting twice gives two independent copies", () => {
    const frag = copyFragment(sample, ["a", "b"]);
    const first = pasteFragment(frag, 40, 40);
    const second = pasteFragment(frag, 80, 80);
    const ids = [...first.nodes, ...second.nodes].map((n) => n.id);
    expect(new Set(ids).size).toBe(4);
  });
});
