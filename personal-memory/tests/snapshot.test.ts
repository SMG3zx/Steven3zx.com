import { expect, test } from "bun:test";
import { validateSnapshot } from "../src/snapshot";

const node = (key: string) => ({ key, labels: ["Event"], properties: { id: key } });

test("snapshot validation accepts linked nodes and relationships", () => {
  expect(validateSnapshot({ format: "personal-memory-snapshot", version: 1, exportedAt: new Date().toISOString(), nodes: [node("a"), node("b")], relationships: [{ sourceKey: "a", targetKey: "b", type: "RELATED_TO", properties: {} }] })).toEqual({ nodes: 2, relationships: 1 });
});

test("snapshot validation rejects dangling relationships", () => {
  expect(() => validateSnapshot({ format: "personal-memory-snapshot", version: 1, exportedAt: new Date().toISOString(), nodes: [node("a")], relationships: [{ sourceKey: "a", targetKey: "missing", type: "RELATED_TO", properties: {} }] })).toThrow("missing node");
});
