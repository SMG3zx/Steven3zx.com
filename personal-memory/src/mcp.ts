import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";

const apiUrl = process.env.MEMORY_API_URL ?? "http://127.0.0.1:4781";
const apiKey = process.env.MEMORY_API_KEY;

async function call(path: string, body?: unknown) {
  const response = await fetch(`${apiUrl}${path}`, {
    method: body === undefined ? "GET" : "POST",
    headers: { "content-type": "application/json", ...(apiKey ? { authorization: `Bearer ${apiKey}` } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await response.json();
  if (!response.ok) {
    const trace = typeof data.traceId === "string" ? ` (trace: ${data.traceId})` : "";
    throw new Error(`${data.error ?? `memory service returned ${response.status}`}${trace}`);
  }
  return data;
}

const server = new McpServer({ name: "personal-memory", version: "0.1.0" });

server.registerTool("remember_event", {
  description: "Persist an immutable user, agent, tool, decision, artifact, or error event. The result includes a traceId receipt for the audit console.",
  inputSchema: {
    kind: z.enum(["user_message", "agent_message", "tool_call", "tool_result", "decision", "artifact", "error"]),
    content: z.string(),
    source: z.string().optional(),
    userId: z.string().optional(),
    projectId: z.string().optional(),
    sessionId: z.string().optional(),
    metadata: z.record(z.string(), z.unknown()).optional(),
  },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call("/v1/events", input), null, 2) }] }));

server.registerTool("remember_events", {
  description: "Persist an ordered batch of immutable user, agent, tool, decision, artifact, or error events. The result includes a traceId receipt for the audit console.",
  inputSchema: { events: z.array(z.object({
    id: z.string().optional(),
    idempotencyKey: z.string().optional(),
    kind: z.enum(["user_message", "agent_message", "tool_call", "tool_result", "decision", "artifact", "error"]),
    content: z.string(),
    source: z.string().optional(),
    userId: z.string().optional(),
    projectId: z.string().optional(),
    sessionId: z.string().optional(),
    occurredAt: z.string().optional(),
    metadata: z.record(z.string(), z.unknown()).optional(),
  })).min(1).max(100) },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call("/v1/events/batch", input), null, 2) }] }));

server.registerTool("remember_conversation", {
  description: "Persist an ordered Codex conversation with durable session and project provenance. Use this for real user, agent, and tool messages; it is separate from test or benchmark data. The result includes a traceId receipt.",
  inputSchema: {
    sessionId: z.string().min(1),
    userId: z.string().optional(),
    projectId: z.string().optional(),
    messages: z.array(z.object({
      id: z.string().optional(),
      idempotencyKey: z.string().optional(),
      kind: z.enum(["user_message", "agent_message", "tool_call", "tool_result", "decision", "artifact", "error"]),
      content: z.string().min(1),
      occurredAt: z.string().optional(),
      metadata: z.record(z.string(), z.unknown()).optional(),
    })).min(1).max(100),
  },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call("/v1/conversations", input), null, 2) }] }));

server.registerTool("reflect_session", {
  description: "Persist a session reflection, lessons learned, and Reflexion-style failure memories without deleting the original events.",
  inputSchema: {
    sessionId: z.string().min(1),
    content: z.string().min(1),
    lessons: z.array(z.string().min(1)).optional(),
    failures: z.array(z.string().min(1)).optional(),
    sourceEventIds: z.array(z.string()).optional(),
    executable: z.boolean().optional(),
    command: z.string().optional(),
    parameters: z.record(z.string(), z.unknown()).optional(),
    safety: z.enum(["read-only", "approval-required", "trusted"]).optional(),
    userId: z.string().optional(),
    projectId: z.string().optional(),
  },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call(`/v1/sessions/${encodeURIComponent(input.sessionId)}/reflection`, input), null, 2) }] }));

server.registerTool("recall_memory", {
  description: "Search durable events and curated memories for relevant context.",
  inputSchema: { query: z.string(), userId: z.string().optional(), projectId: z.string().optional(), sessionId: z.string().optional(), includeHistorical: z.boolean().optional(), includeTestData: z.boolean().optional(), limit: z.number().int().min(1).max(50).optional() },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call("/v1/recall", input), null, 2) }] }));

server.registerTool("build_context", {
  description: "Retrieve a bounded, provenance-preserving context pack ready for agent prompt injection.",
  inputSchema: { query: z.string(), userId: z.string().optional(), projectId: z.string().optional(), sessionId: z.string().optional(), includeHistorical: z.boolean().optional(), limit: z.number().int().min(1).max(50).optional(), maxChars: z.number().int().min(256).max(100000).optional() },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call("/v1/context", input), null, 2) }] }));

server.registerTool("create_memory", {
  description: "Create a curated durable memory with optional provenance links to source events.",
  inputSchema: {
    content: z.string(),
    category: z.enum(["working", "profile", "fact", "preference", "decision", "episode", "belief", "reflection", "failure", "semantic", "procedure", "summary", "claim"]).optional(),
    status: z.enum(["active", "superseded", "retracted"]).optional(),
    confidence: z.number().min(0).max(1).optional(),
    metadata: z.record(z.string(), z.unknown()).optional(),
    userId: z.string().optional(),
    projectId: z.string().optional(),
    sessionId: z.string().optional(),
    sourceEventIds: z.array(z.string()).optional(),
    entityIds: z.array(z.string()).optional(),
    supersedesMemoryId: z.string().optional(),
    subject: z.string().optional(),
    predicate: z.string().optional(),
    object: z.string().optional(),
    validFrom: z.string().optional(),
    validUntil: z.string().optional(),
  },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call("/v1/memories", input), null, 2) }] }));

server.registerTool("retract_memory", {
  description: "Retract a curated memory without deleting its event or provenance history.",
  inputSchema: { id: z.string(), reason: z.string().optional() },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call(`/v1/memories/${encodeURIComponent(input.id)}/retract`, input.reason ? { reason: input.reason } : {}), null, 2) }] }));

server.registerTool("record_memory_utility", {
  description: "Record whether a retrieved memory was useful so future ranking can learn from actual use.",
  inputSchema: { id: z.string(), useful: z.boolean(), feedback: z.string().optional() },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call(`/v1/memories/${encodeURIComponent(input.id)}/utility`, input), null, 2) }] }));

server.registerTool("archive_memory", {
  description: "Archive a selected durable memory while preserving immutable events and provenance links.",
  inputSchema: { id: z.string(), reason: z.string().optional() },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call(`/v1/memories/${encodeURIComponent(input.id)}/archive`, input.reason ? { reason: input.reason } : {}), null, 2) }] }));

server.registerTool("upsert_entity", {
  description: "Create or update a canonical entity used to connect memories and events. The result includes a traceId receipt for the audit console.",
  inputSchema: { name: z.string(), type: z.enum(["person", "project", "organization", "technology", "file", "tool", "concept", "other"]), aliases: z.array(z.string()).optional(), description: z.string().optional(), userId: z.string().optional(), projectId: z.string().optional(), sessionId: z.string().optional() },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call("/v1/entities", input), null, 2) }] }));

server.registerTool("upsert_document", {
  description: "Persist a source document that can be chunked and cited by claims. The result includes a traceId receipt.",
  inputSchema: {
    title: z.string(),
    source: z.string(),
    content: z.string().optional(),
    metadata: z.record(z.string(), z.unknown()).optional(),
    userId: z.string().optional(),
    projectId: z.string().optional(),
    sessionId: z.string().optional(),
  },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call("/v1/documents", input), null, 2) }] }));

server.registerTool("upsert_chunk", {
  description: "Persist a source chunk and link it to a document and optional entities.",
  inputSchema: {
    documentId: z.string(),
    content: z.string(),
    ordinal: z.number().int().min(0),
    embedding: z.array(z.number()).optional(),
    entityIds: z.array(z.string()).optional(),
    userId: z.string().optional(),
    projectId: z.string().optional(),
    sessionId: z.string().optional(),
  },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call("/v1/chunks", input), null, 2) }] }));

server.registerTool("create_claim", {
  description: "Create a structured claim between entities with source chunk/event provenance.",
  inputSchema: {
    statement: z.string(),
    predicate: z.string(),
    subjectEntityId: z.string(),
    objectEntityId: z.string().optional(),
    confidence: z.number().min(0).max(1).optional(),
    sourceChunkIds: z.array(z.string()).optional(),
    sourceEventIds: z.array(z.string()).optional(),
    userId: z.string().optional(),
    projectId: z.string().optional(),
    sessionId: z.string().optional(),
  },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call("/v1/claims", input), null, 2) }] }));

server.registerTool("create_procedure", {
  description: "Persist a source-backed procedural runbook for a repeatable task.",
  inputSchema: {
    title: z.string(),
    goal: z.string(),
    steps: z.array(z.string()).min(1),
    preconditions: z.array(z.string()).optional(),
    knownFailures: z.array(z.string()).optional(),
    confidence: z.number().min(0).max(1).optional(),
    userId: z.string().optional(),
    projectId: z.string().optional(),
    sessionId: z.string().optional(),
    sourceEventIds: z.array(z.string()).optional(),
    executable: z.boolean().optional(),
    command: z.string().optional(),
    parameters: z.record(z.string(), z.unknown()).optional(),
    safety: z.enum(["read-only", "approval-required", "trusted"]).optional(),
  },
}, async (input) => ({ content: [{ type: "text", text: JSON.stringify(await call("/v1/procedures", input), null, 2) }] }));

await server.connect(new StdioServerTransport());
