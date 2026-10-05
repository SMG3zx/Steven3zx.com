import { expect, test } from "bun:test";
import { buildContextPack, orderContextCandidates } from "../src/context";

test("context packs preserve provenance and enforce a deterministic character budget", () => {
  const pack = buildContextPack([
    { id: "m1", type: "memory", content: "first", sourceEventIds: ["e1"] },
    { id: "m2", type: "memory", content: "second", sourceChunkIds: ["c2"] },
  ], 50);
  expect(pack.context).toContain("memory:m1");
  expect(pack.context).toContain("sources: e1");
  expect(pack.truncated).toBe(true);
  expect(pack.omittedResults).toBe(1);
  expect(pack.characterCount).toBeLessThanOrEqual(50);
});

test("context ordering keeps top evidence at the edges and removes duplicates", () => {
  const ordered = orderContextCandidates([
    { type: "memory", id: "one", content: "one" },
    { type: "memory", id: "two", content: "two" },
    { type: "memory", id: "three", content: "three" },
    { type: "memory", id: "two", content: "duplicate" },
  ]);
  expect(ordered.map((item) => item.id)).toEqual(["one", "three", "two"]);
});
