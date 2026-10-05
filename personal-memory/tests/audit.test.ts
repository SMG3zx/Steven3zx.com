import { describe, expect, test } from "bun:test";
import { AuditRegistry, redact } from "../src/audit";

describe("audit registry", () => {
  test("records hierarchical runs and redacts credentials", () => {
    const registry = new AuditRegistry();
    const root = registry.start({ name: "chat.turn", kind: "chain", input: { prompt: "hello", apiKey: "sk-secret-value-123456" } });
    const child = registry.start({ name: "memory.recall", kind: "tool", traceId: root.traceId, parentRunId: root.id, input: { query: "hello" } });
    registry.finish(child, { authorization: "Bearer secret-value-123456" });
    registry.finish(root, { answer: "hi" });

    const trace = registry.trace(root.traceId);
    expect(trace).toHaveLength(2);
    expect(trace[0]?.parentRunId).toBeUndefined();
    expect(trace[1]?.parentRunId).toBe(root.id);
    expect(JSON.stringify(trace)).not.toContain("secret-value-123456");
    expect(trace.every((run) => run.status === "success")).toBe(true);
  });

  test("filters recent runs", () => {
    const registry = new AuditRegistry();
    const run = registry.start({ name: "security.block", kind: "security" });
    registry.fail(run, new Error("blocked"));
    expect(registry.query({ status: "error", kind: "security" })).toHaveLength(1);
    expect(redact({ password: "do-not-store", nested: { token: "also-private" } })).toEqual({ password: "[REDACTED]", nested: { token: "[REDACTED]" } });
  });
});
