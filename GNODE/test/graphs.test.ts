import { describe, expect, test } from "bun:test";
import { mkdtemp, readdir, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { saveGraphIn } from "../src/server/graphs";
import { emptyGraph, type Graph } from "../src/shared/graph";

function graphWith(name: string, x: number): Graph {
  return { ...emptyGraph(name), nodes: [{ id: "a", kind: "main", x, y: 0, params: {} }] };
}

describe("saveGraphIn", () => {
  test("a first save writes the file and makes no backup", async () => {
    const dir = await mkdtemp(join(tmpdir(), "gnode-graphs-"));
    await saveGraphIn(dir, "demo", graphWith("demo", 1));
    expect(await readdir(dir)).toEqual(["demo.json"]);
  });

  test("saving the same text again makes no backup", async () => {
    const dir = await mkdtemp(join(tmpdir(), "gnode-graphs-"));
    await saveGraphIn(dir, "demo", graphWith("demo", 1));
    await saveGraphIn(dir, "demo", graphWith("demo", 1));
    const hasBackups = await readdir(join(dir, "backups")).then(() => true, () => false);
    expect(hasBackups).toBe(false);
  });

  test("changing a saved program keeps the old version in backups", async () => {
    const dir = await mkdtemp(join(tmpdir(), "gnode-graphs-"));
    await saveGraphIn(dir, "demo", graphWith("demo", 1));
    const before = await readFile(join(dir, "demo.json"), "utf8");
    await saveGraphIn(dir, "demo", graphWith("demo", 2));
    const backups = await readdir(join(dir, "backups", "demo"));
    expect(backups).toHaveLength(1);
    expect(await readFile(join(dir, "backups", "demo", backups[0]), "utf8")).toBe(before);
    expect(JSON.parse(await readFile(join(dir, "demo.json"), "utf8")).nodes[0].x).toBe(2);
  });
});
