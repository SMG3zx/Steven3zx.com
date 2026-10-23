import { describe, expect, test } from "bun:test";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { listGraphsIn, readGraphIn, saveGraphIn } from "../src/server/graphs";
import { emptyGraph, type Graph } from "../src/shared/graph";

const group: Graph = {
  ...emptyGraph("pair"),
  nodes: [
    { id: "a", kind: "literal", x: 0, y: 0, params: { type: "int", value: "1" } },
    { id: "b", kind: "print", x: 180, y: 0, params: {} },
  ],
  wires: [{ id: "w", from: { node: "a", port: "value" }, to: { node: "b", port: "value" } }],
};

describe("block groups", () => {
  test("a saved group lists by name and reads back as it was saved", async () => {
    const dir = await mkdtemp(join(tmpdir(), "gnode-groups-"));
    await saveGraphIn(dir, "pair", group);
    expect(await listGraphsIn(dir)).toEqual(["pair"]);
    expect(await readGraphIn(dir, "pair")).toEqual(group);
  });

  test("the first save creates the folder", async () => {
    const dir = join(await mkdtemp(join(tmpdir(), "gnode-groups-")), "groups");
    await saveGraphIn(dir, "pair", group);
    expect(await listGraphsIn(dir)).toEqual(["pair"]);
  });

  test("a missing folder lists as no groups", async () => {
    expect(await listGraphsIn(join(tmpdir(), "gnode-no-such-folder"))).toEqual([]);
  });
});
