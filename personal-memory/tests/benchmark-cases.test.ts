import { expect, test } from "bun:test";
import { parseBenchmarkCases } from "../src/benchmark-cases";

test("benchmark loader accepts external JSON and normalizes question/gold fields", () => {
  const cases = parseBenchmarkCases(JSON.stringify([{ id: "bright-1", category: "bright-reasoning", question: "Which tool?", gold: ["neo4j"] }]));
  expect(cases[0]).toMatchObject({ id: "bright-1", query: "Which tool?", expectedContains: ["neo4j"], requiresProvenance: true, reasoningIntensive: true });
});

test("benchmark loader accepts JSONL reasoning cases", () => {
  const cases = parseBenchmarkCases('{"query":"What changed?","answer":"streaming","category":"temporal"}\n{"query":"Why?","expected":["because"],"category":"temporal"}', "jsonl");
  expect(cases).toHaveLength(2);
  expect(cases[0]?.expectedContains).toEqual(["streaming"]);
  expect(cases[1]?.category).toBe("temporal");
});
