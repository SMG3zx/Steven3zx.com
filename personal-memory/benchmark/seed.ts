const apiUrl = (process.env.MEMORY_API_URL ?? "http://127.0.0.1:4781").replace(/\/$/, "");
const apiKey = process.env.MEMORY_API_KEY;
const projectId = "benchmark-suite";

async function call(path: string, body: unknown) {
  const response = await fetch(`${apiUrl}${path}`, {
    method: "POST",
    headers: { "content-type": "application/json", ...(apiKey ? { authorization: `Bearer ${apiKey}` } : {}) },
    body: JSON.stringify(body),
  });
  const data = await response.json();
  if (!response.ok) throw new Error(`${path}: ${data.error ?? response.status}`);
  return data;
}

const event = await call("/v1/events", { id: "benchmark-event-embedding-decision", kind: "decision", content: "The benchmark project decided to use Qwen3 embeddings for local semantic retrieval.", source: "benchmark", projectId, metadata: { benchmark: true } });
const laterEvent = await call("/v1/events", { id: "benchmark-event-later-session", kind: "user_message", content: "In a later session, the benchmark project confirmed that Qwen3 remains the embedding provider.", source: "benchmark", projectId, sessionId: "benchmark-session-2", metadata: { benchmark: true } });
const reflectionEvent = await call("/v1/events", { id: "benchmark-event-reflection", kind: "decision", content: "Reflection: retrieval quality improved when graph evidence and source citations were combined.", source: "benchmark", projectId, sessionId: "benchmark-session-2", metadata: { benchmark: true } });
const failureEvent = await call("/v1/events", { id: "benchmark-event-failure", kind: "error", content: "Failure lesson: an invalid model endpoint prevents the memory worker from consolidating events.", source: "benchmark", projectId, sessionId: "benchmark-session-2", metadata: { benchmark: true, status: "failed" } });
const entity = await call("/v1/entities", { id: "benchmark-project", name: "Benchmark Project", type: "project", projectId });
const document = await call("/v1/documents", { id: "benchmark-document", title: "Benchmark source", source: "benchmark-fixture", content: "The benchmark source supports the Qwen3 embedding decision.", projectId });
const chunk = await call("/v1/chunks", { id: "benchmark-chunk", documentId: document.id, content: "Benchmark Project uses Qwen3 embeddings for local retrieval.", ordinal: 0, entityIds: [entity.id], projectId });
await call("/v1/claims", { id: "benchmark-claim", statement: "Benchmark Project uses Qwen3 embeddings.", predicate: "USES", subjectEntityId: entity.id, sourceChunkIds: [chunk.id], projectId });
await call("/v1/memories", { id: "benchmark-memory-fact", content: "The benchmark project uses Qwen3 embeddings for local semantic retrieval.", category: "decision", subject: "Benchmark Project", predicate: "USES", object: "Qwen3 embeddings", projectId, sourceEventIds: [event.id] });
await call("/v1/memories", { id: "benchmark-memory-multisession", content: "Across multiple sessions, the benchmark project confirmed that Qwen3 remains the embedding provider.", category: "semantic", subject: "Benchmark Project", predicate: "CONFIRMED_PROVIDER", object: "Qwen3", projectId, sourceEventIds: [event.id, laterEvent.id] });
const oldMemory = await call("/v1/memories", { id: "benchmark-memory-old", content: "Before Qwen3, the benchmark project used a hosted embedding API.", category: "fact", subject: "Benchmark Project", predicate: "EMBEDDING_PROVIDER", object: "hosted embedding API", validFrom: "2023-01-01T00:00:00.000Z", projectId, sourceEventIds: [event.id] });
await call("/v1/memories", { id: "benchmark-memory-current", content: "The current benchmark embedding provider is Qwen3.", category: "fact", subject: "Benchmark Project", predicate: "EMBEDDING_PROVIDER", object: "Qwen3", validFrom: "2024-01-01T00:00:00.000Z", projectId, sourceEventIds: [event.id], supersedesMemoryId: oldMemory.id });
await call("/v1/procedures", { id: "benchmark-procedure", title: "Recover the memory worker", goal: "Restore the worker after a failed run.", steps: ["Inspect failed jobs", "Restart the worker", "Verify the queue drains"], preconditions: ["Neo4j is reachable"], knownFailures: ["Invalid model endpoint"], projectId, sourceEventIds: [event.id] });
await call("/v1/memories", { id: "benchmark-memory-reflection", content: "Reflection: retrieval quality improved when graph evidence and source citations were combined.", category: "reflection", projectId, sessionId: "benchmark-session-2", sourceEventIds: [reflectionEvent.id] });
await call("/v1/memories", { id: "benchmark-memory-belief", content: "The benchmark project believes durable source-linked memories should outrank unsupported recollections.", category: "belief", projectId, sourceEventIds: [event.id] });
await call("/v1/memories", { id: "benchmark-memory-failure", content: "Failure lesson: an invalid model endpoint prevents the memory worker from consolidating events.", category: "failure", projectId, sourceEventIds: [failureEvent.id], metadata: { memoryKind: "reflexion_failure" } });
await call("/v1/procedures", { id: "benchmark-executable-skill", title: "Inspect memory health", goal: "Check the memory API before retrieval.", steps: ["Call the health endpoint", "Verify Neo4j connectivity"], executable: true, command: "GET /health", parameters: { timeoutMs: 3000 }, safety: "read-only", projectId, sourceEventIds: [event.id] });
await call("/v1/memories", { id: "benchmark-memory-archived", content: "Archived benchmark-only secret: retired provider token alpha-991.", category: "working", status: "archived", projectId, sourceEventIds: [event.id] });
console.log(JSON.stringify({ apiUrl, projectId, seeded: true }));
