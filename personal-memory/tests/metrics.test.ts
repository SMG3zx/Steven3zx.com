import { expect, test } from "bun:test";
import { MetricsRegistry } from "../src/metrics";

test("metrics retain bounded retrieval telemetry without content", () => {
  const metrics = new MetricsRegistry();
  metrics.recordRetrieval({ route: "semantic", methods: ["lexical", "graph"], durationMs: 12, ok: true, estimatedCostUsd: 0.0025 });
  metrics.recordRetrieval({ route: "temporal", methods: ["lexical"], durationMs: 30, ok: false });
  metrics.recordAnswerValidation(true);
  metrics.recordAnswerValidation(false);
  const snapshot = metrics.snapshot();
  expect(snapshot.retrievals.count).toBe(2);
  expect(snapshot.retrievals.errors).toBe(1);
  expect(snapshot.retrievals.routes.temporal).toBe(1);
  expect(snapshot.retrievals.methods.graph).toBe(1);
  expect(snapshot.retrievals.p95LatencyMs).toBe(30);
  expect(snapshot.retrievals.estimatedCostUsd).toBe(0.0025);
  expect(snapshot.answerValidation.rejected).toBe(1);
  expect(JSON.stringify(snapshot).includes("Neo4j")).toBe(false);
});
