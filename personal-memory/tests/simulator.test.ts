import { describe, expect, test } from "bun:test";
import { createSimulation, evaluateSimulation, runOfflineSimulationMatrix, selfTestSimulation, toOpenTelemetryTraces } from "../src/simulator";

describe("research-backed simulation", () => {
  test("is deterministic for a fixed seed and changes worlds for different seeds", () => {
    expect(createSimulation(7)).toEqual(createSimulation(7));
    expect(createSimulation(7).projectId).not.toBe(createSimulation(8).projectId);
  });

  test("creates temporal, update, failure, forgetting, and security scenarios", () => {
    const world = createSimulation(42);
    expect(world.events.length).toBeGreaterThanOrEqual(9);
    expect(world.turns.some((turn) => turn.userIntent === "correct")).toBe(true);
    expect(world.turns.filter((turn) => turn.expectedProbeId).length).toBeGreaterThanOrEqual(3);
    expect(world.probes.map((probe) => probe.category)).toEqual(["update", "temporal", "fact", "contradiction", "contradiction", "forgetting", "security"]);
    expect(world.events.some((event) => !event.trusted && event.kind === "malicious_memory")).toBe(true);
  });

  test("independent oracle scores complete answers and catches leaked secrets", () => {
    const world = createSimulation(42);
    const complete = selfTestSimulation(42);
    expect(complete.hitRate).toBe(1);
    expect(complete.provenanceRate).toBe(1);
    expect(complete.securityRate).toBe(1);
    expect(complete.meanLatencyMs).toBe(0);
    expect(complete.estimatedCostUsd).toBe(0);
    const leaked = evaluateSimulation(world, [{ probeId: "sim-probe-security", answer: "atlas-secret-991", memoryIds: [world.events.at(-1)!.memoryId], abstain: false }]);
    expect(leaked.results.find((result) => result.probeId === "sim-probe-security")?.securityCorrect).toBe(false);
  });

  test("runs reproducible offline regression matrices with a versioned dataset", () => {
    const reports = runOfflineSimulationMatrix([1, 2, 3]);
    expect(reports.every((report) => report.datasetVersion === "sim-v1" && report.hitRate === 1 && report.securityRate === 1)).toBe(true);
    expect(runOfflineSimulationMatrix([1])).toEqual(runOfflineSimulationMatrix([1]));
  });

  test("exports stable OpenTelemetry-shaped trace envelopes without external dependencies", () => {
    const envelope = toOpenTelemetryTraces([{ traceId: "a".repeat(32), spanId: "b".repeat(16), name: "probe", startTimeUnixNano: "1", endTimeUnixNano: "2", attributes: { "simulation.hit": true, "simulation.latency_ms": 3 } }]);
    expect(envelope.resourceSpans[0].resource.attributes[0].value.stringValue).toBe("personal-memory-simulator");
    expect(envelope.resourceSpans[0].scopeSpans[0].spans[0].traceId).toBe("a".repeat(32));
  });
});
