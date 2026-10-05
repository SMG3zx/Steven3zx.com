import { expect, test } from "bun:test";
import { failureMemoryFromEvent } from "../src/worker";
import { ContentTooLargeError, DEFAULT_MAX_CONTENT_BYTES, validateContent } from "../src/content-limits";

test("failure events become provenance-linked Reflexion-style episode memories", () => {
  const memory = failureMemoryFromEvent({
    id: "event-1",
    kind: "tool_result",
    content: "Deployment timed out after 30 seconds",
    projectId: "project-1",
    sessionId: "session-1",
    metadata: { command: "deploy", status: "failed" },
  });
  expect(memory?.category).toBe("episode");
  expect(memory?.sourceEventIds).toEqual(["event-1"]);
  expect(memory?.metadata).toMatchObject({ memoryKind: "reflexion_failure" });
});

test("successful tool results do not create failure memories", () => {
  expect(failureMemoryFromEvent({ kind: "tool_result", content: "Deployment completed", metadata: { status: "success" } })).toBeNull();
});

test("content limits use UTF-8 bytes and reject oversized values before persistence", () => {
  expect(validateContent("🙂")).toBe(4);
  expect(() => validateContent("x".repeat(DEFAULT_MAX_CONTENT_BYTES + 1))).toThrow(ContentTooLargeError);
});
