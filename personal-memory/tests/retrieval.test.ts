import { expect, test } from "bun:test";
import { assessEvidence, classifyQueryComplexity, correctiveQuery, memoryRetentionScore, retrievalPlan, routeQuery, shouldAbstain } from "../src/retrieval";

test("routes temporal, procedural, graph, and semantic queries", () => {
  expect(routeQuery("What did we use before Neo4j?")).toBe("temporal");
  expect(routeQuery("How do I recover the deployment?")).toBe("procedural");
  expect(routeQuery("Which projects depend on Ollama?")).toBe("graph");
  expect(routeQuery("What did we decide about embeddings?")).toBe("semantic");
});

test("abstains when evidence is absent or weak", () => {
  expect(shouldAbstain([]).abstain).toBe(true);
  expect(shouldAbstain([{ score: 0.1 }]).abstain).toBe(true);
  expect(shouldAbstain([{ score: 0.8 }]).abstain).toBe(false);
  expect(shouldAbstain([{ score: 1.2, content: "unrelated evidence", retrievalMethod: "lexical" }], "Zyx-Not-Real-991").abstain).toBe(true);
  expect(shouldAbstain([{ score: 1.2, content: "The benchmark provider is Qwen3", retrievalMethod: "lexical" }], "Archived token alpha-991").abstain).toBe(true);
});

test("builds a corrective query and assesses retrieval evidence", () => {
  expect(correctiveQuery("What did we decide about Qwen embeddings?")).toBe("decide qwen embeddings");
  expect(correctiveQuery("qwen embeddings")).toBe(null);
  expect(assessEvidence([{ score: 0.1 }]).needsCorrection).toBe(true);
  expect(assessEvidence([{ score: 0.8 }]).needsCorrection).toBe(false);
});

test("retention decays by category and reinforcement slows forgetting", () => {
  const now = new Date("2026-01-31T00:00:00.000Z");
  const recent = memoryRetentionScore({ occurredAt: "2026-01-30T00:00:00.000Z", category: "fact", confidence: 1, now });
  const oldEpisode = memoryRetentionScore({ occurredAt: "2025-01-01T00:00:00.000Z", category: "episode", confidence: 1, now });
  const reinforced = memoryRetentionScore({ occurredAt: "2025-01-01T00:00:00.000Z", category: "episode", confidence: 1, reinforcementCount: 5, now });
  expect(recent).toBeGreaterThan(oldEpisode);
  expect(reinforced).toBeGreaterThan(oldEpisode);
  expect(memoryRetentionScore({ occurredAt: "2026-01-30T00:00:00.000Z", category: "fact", confidence: 0.4, now })).toBeLessThan(recent);
});

test("retrieval budgets scale with query complexity", () => {
  expect(classifyQueryComplexity("What is the deployment status?")).toBe("simple");
  expect(classifyQueryComplexity("Which projects depend on Ollama and affect embeddings?")).toBe("multi-hop");
  expect(retrievalPlan("Which projects depend on Ollama and affect embeddings?").candidateMultiplier)
    .toBeGreaterThan(retrievalPlan("What is the deployment status?").candidateMultiplier);
});
