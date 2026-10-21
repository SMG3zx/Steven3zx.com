import { describe, expect, test } from "bun:test";
import { chainOf, emptyGraph, parseGraph, type Graph } from "../src/shared/graph";

// main -> print <- literal, and print -> loop; an unrelated block sits alone.
const sample: Graph = {
  ...emptyGraph("sample"),
  nodes: [
    { id: "main", kind: "main", x: 0, y: 0, params: {} },
    { id: "print", kind: "print", x: 0, y: 0, params: {} },
    { id: "lit", kind: "literal", x: 0, y: 0, params: {} },
    { id: "loop", kind: "loop", x: 0, y: 0, params: {} },
    { id: "alone", kind: "literal", x: 0, y: 0, params: {} },
  ],
  wires: [
    { id: "w1", from: { node: "main", port: "body" }, to: { node: "print", port: "in" } },
    { id: "w2", from: { node: "lit", port: "value" }, to: { node: "print", port: "value" } },
    { id: "w3", from: { node: "print", port: "out" }, to: { node: "loop", port: "in" } },
  ],
};

describe("chainOf", () => {
  test("takes in everything feeding a block and everything it feeds", () => {
    const chain = chainOf(sample, "print");
    expect([...chain.nodes].sort()).toEqual(["lit", "loop", "main", "print"]);
    expect([...chain.wires].sort()).toEqual(["w1", "w2", "w3"]);
  });

  test("leaves out blocks with no wire to the chain", () => {
    expect(chainOf(sample, "print").nodes.has("alone")).toBe(false);
  });

  test("a block with no wires is a chain of one", () => {
    const chain = chainOf(sample, "alone");
    expect([...chain.nodes]).toEqual(["alone"]);
    expect(chain.wires.size).toBe(0);
  });
});

describe("parseGraph bends", () => {
  test("accepts wires with numeric bends", () => {
    const g = parseGraph({
      ...sample,
      wires: [{ ...sample.wires[0], bends: [{ dx: 10, dy: -4 }] }],
    });
    expect(g.wires[0].bends).toEqual([{ dx: 10, dy: -4 }]);
  });

  test("rejects a bend without numbers", () => {
    expect(() =>
      parseGraph({ ...sample, wires: [{ ...sample.wires[0], bends: [{ dx: "far", dy: 0 }] }] }),
    ).toThrow("bend");
  });
});
