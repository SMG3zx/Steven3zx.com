import { describe, expect, test } from "bun:test";
import { copyFragment, emptyGraph, pasteFragment, parseGraph, type Graph } from "../src/shared/graph";

const nested: Graph = {
  ...emptyGraph("nested"),
  nodes: [
    { id: "site", kind: "trashSite", x: 100, y: 100, params: {} },
    { id: "cl", kind: "trashCleaner", x: 0, y: 0, params: {}, parent: "site", slot: 0 },
    { id: "p", kind: "print", x: 400, y: 100, params: {} },
  ],
  wires: [{ id: "w", from: { node: "cl", port: "cleaner" }, to: { node: "p", port: "value" } }],
};

describe("blocks in a container", () => {
  test("copying a container copies the blocks in its slots with it", () => {
    const frag = copyFragment(nested, ["site", "cl"]);
    expect(frag.nodes.map((n) => n.id)).toEqual(["site", "cl"]);
  });

  test("copying a block in a slot without its container leaves it out", () => {
    const frag = copyFragment(nested, ["cl", "p"]);
    expect(frag.nodes.map((n) => n.id)).toEqual(["p"]);
  });

  test("pasting keeps a block in its slot, under the container's new id", () => {
    const frag = copyFragment(nested, ["site", "cl"]);
    const pasted = pasteFragment(frag, 40, 40);
    const site = pasted.nodes.find((n) => n.kind === "trashSite")!;
    const cl = pasted.nodes.find((n) => n.kind === "trashCleaner")!;
    expect(cl.parent).toBe(site.id);
    expect(cl.slot).toBe(0);
    expect(site.id).not.toBe("site");
  });

  test("a file with a slot that is not a number from 0 is refused", () => {
    const bad = { ...nested, nodes: [nested.nodes[0], { ...nested.nodes[1], slot: -1 }] };
    expect(() => parseGraph(bad)).toThrow("slot");
  });

  test("a file with a parent that is not an id is refused", () => {
    const bad = { ...nested, nodes: [nested.nodes[0], { ...nested.nodes[1], parent: 7 }] };
    expect(() => parseGraph(bad)).toThrow("parent");
  });

  test("a container and its blocks survive a save and open", () => {
    expect(parseGraph(JSON.parse(JSON.stringify(nested)))).toEqual(nested);
  });
});
