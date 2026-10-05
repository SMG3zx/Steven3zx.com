const baseUrl = (process.env.MCP_SMOKE_URL ?? "http://127.0.0.1:4781").replace(/\/$/, "");
const child = Bun.spawn(["bun", "run", "mcp"], {
  stdin: "pipe",
  stdout: "pipe",
  stderr: "pipe",
  env: { ...process.env, MEMORY_API_URL: baseUrl },
});
const reader = child.stdout.getReader();
const decoder = new TextDecoder();
let buffer = "";

async function nextMessage(timeoutMs = 5_000) {
  const deadline = Date.now() + timeoutMs;
  while (true) {
    const newline = buffer.indexOf("\n");
    if (newline >= 0) {
      const line = buffer.slice(0, newline).replace(/\r$/, "");
      buffer = buffer.slice(newline + 1);
      if (line.trim()) return JSON.parse(line) as Record<string, unknown>;
    }
    const remaining = Math.max(1, deadline - Date.now());
    const result = await Promise.race([
      reader.read(),
      new Promise<never>((_, reject) => setTimeout(() => reject(new Error("Timed out waiting for MCP response")), remaining)),
    ]);
    if (result.done) throw new Error("MCP process closed stdout before responding");
    buffer += decoder.decode(result.value, { stream: true });
  }
}

async function send(message: Record<string, unknown>) {
  child.stdin.write(`${JSON.stringify(message)}\n`);
  child.stdin.flush();
  return nextMessage();
}

let requestId = 10;
async function callTool(name: string, args: Record<string, unknown>) {
  const result = await send({ jsonrpc: "2.0", id: requestId++, method: "tools/call", params: { name, arguments: args } });
  if (result.error) throw new Error(`MCP ${name} failed: ${JSON.stringify(result.error)}`);
  const text = (result.result as { content?: Array<{ type?: string; text?: string }> } | undefined)?.content?.find((item) => item.type === "text")?.text;
  if (!text) throw new Error(`MCP ${name} returned no text content`);
  const data = JSON.parse(text) as Record<string, unknown>;
  if (typeof data.traceId !== "string") throw new Error(`MCP ${name} response did not include a traceId`);
  return data;
}

try {
  const initialize = await send({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "personal-memory-mcp-smoke", version: "1.0.0" } } });
  if (initialize.error) throw new Error(`MCP initialize failed: ${JSON.stringify(initialize.error)}`);
  const listed = await send({ jsonrpc: "2.0", id: 2, method: "tools/list", params: {} });
  const tools = (listed.result as { tools?: Array<{ name?: string }> } | undefined)?.tools ?? [];
  if (!tools.some((tool) => tool.name === "remember_event")) throw new Error("MCP tools/list did not expose remember_event");
  const suffix = crypto.randomUUID();
  const scope = { userId: `mcp-smoke-${suffix}`, projectId: `mcp-smoke-project-${suffix}`, sessionId: `mcp-smoke-session-${suffix}` };
  const event = await callTool("remember_event", { ...scope, kind: "artifact", content: `MCP protocol smoke ${suffix}`, source: "mcp-smoke", metadata: { synthetic: true } });
  const batch = await callTool("remember_events", { events: [{ ...scope, kind: "tool_result", content: "MCP batch smoke", source: "mcp-smoke" }] });
  const conversation = await callTool("remember_conversation", { ...scope, messages: [{ kind: "user_message", content: "MCP conversation smoke" }, { kind: "tool_result", content: "MCP tool result smoke" }] });
  const reflection = await callTool("reflect_session", { ...scope, content: "MCP reflection smoke", lessons: ["MCP smoke lesson"], failures: ["MCP smoke failure"] });
  const recall = await callTool("recall_memory", { ...scope, query: "MCP protocol smoke", limit: 5 });
  const context = await callTool("build_context", { ...scope, query: "MCP protocol smoke", maxChars: 512 });
  const entity = await callTool("upsert_entity", { ...scope, name: "MCP smoke entity "+suffix, type: "concept" });
  const document = await callTool("upsert_document", { ...scope, title: "MCP smoke document", source: "mcp-smoke", content: "MCP source document" });
  const chunk = await callTool("upsert_chunk", { ...scope, documentId: String(document.id), content: "MCP source chunk", ordinal: 0 });
  const claim = await callTool("create_claim", { ...scope, statement: "MCP smoke claim", predicate: "supports", subjectEntityId: String(entity.id), sourceChunkIds: [String(chunk.id)], sourceEventIds: [String(event.id)] });
  const procedure = await callTool("create_procedure", { ...scope, title: "MCP smoke procedure", goal: "Exercise MCP procedure flow", steps: ["Run smoke", "Inspect receipt"], sourceEventIds: [String(event.id)] });
  const memory = await callTool("create_memory", { ...scope, content: "MCP smoke durable memory", category: "fact", sourceEventIds: [String(event.id)] });
  const utility = await callTool("record_memory_utility", { id: String(memory.id), useful: true, feedback: "mcp-smoke" });
  const archived = await callTool("archive_memory", { id: String(memory.id), reason: "mcp-smoke" });
  const retractMemory = await callTool("create_memory", { ...scope, content: "MCP smoke retractable memory", category: "fact", sourceEventIds: [String(event.id)] });
  const retracted = await callTool("retract_memory", { id: String(retractMemory.id), reason: "mcp-smoke" });
  console.log(JSON.stringify({ ok: true, initialize: Boolean(initialize.result), toolCount: tools.length, exercisedTools: ["remember_event", "remember_events", "remember_conversation", "reflect_session", "recall_memory", "build_context", "upsert_entity", "upsert_document", "upsert_chunk", "create_claim", "create_procedure", "create_memory", "record_memory_utility", "archive_memory", "retract_memory"], receipts: { event: event.traceId, batch: batch.traceId, conversation: conversation.traceId, reflection: reflection.traceId, recall: recall.traceId, context: context.traceId, entity: entity.traceId, document: document.traceId, chunk: chunk.traceId, claim: claim.traceId, procedure: procedure.traceId, memory: memory.traceId, utility: utility.traceId, archived: archived.traceId, retracted: retracted.traceId } }, null, 2));
} finally {
  child.kill();
}
