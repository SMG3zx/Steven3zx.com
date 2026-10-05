import { expect, test } from "bun:test";
import { reciprocalRankFuse } from "../src/rank-fusion";

const weights = { lexical: 0.4, vector: 0.4, graph: 0.2 } as const;

test("rank fusion keeps incompatible raw index scores separate", () => {
  const results = reciprocalRankFuse({
    lexical: [{ type: "memory", id: "lexical", content: "lexical", score: 100 }],
    vector: [{ type: "memory", id: "vector", content: "vector", score: 0.99 }],
    graph: [],
  }, weights);
  expect(results[0]?.id).toBe("lexical");
  expect(results[0]?.score).toBeCloseTo(0.4 / 61);
});

test("rank fusion rewards agreement across retrieval methods", () => {
  const results = reciprocalRankFuse({
    lexical: [
      { type: "memory", id: "shared", content: "shared", score: 5 },
      { type: "memory", id: "lexical-only", content: "lexical-only", score: 4 },
    ],
    vector: [{ type: "memory", id: "shared", content: "shared", score: 0.8 }],
    graph: [{ type: "memory", id: "shared", content: "shared", score: 0.5 }],
  }, weights);
  expect(results[0]?.id).toBe("shared");
  expect(results[0]?.retrievalMethod).toBe("hybrid");
  expect(results[0]?.retrievalMethods).toEqual(["lexical", "vector", "graph"]);
});
