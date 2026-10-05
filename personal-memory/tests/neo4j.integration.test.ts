import { afterAll, describe, expect, test } from "bun:test";
import { Neo4jStore } from "../src/neo4j-store";
import { ConsolidationWorker } from "../src/worker";

const enabled = process.env.RUN_NEO4J_TESTS === "1";

describe.skipIf(!enabled)("Neo4j persistence integration", () => {
  const store = enabled ? new Neo4jStore() : (null as unknown as Neo4jStore);
  const suffix = crypto.randomUUID();
  const eventId = `integration-event-${suffix}`;
  const entityId = `integration-entity-${suffix}`;
  const documentId = `integration-document-${suffix}`;
  const chunkId = `integration-chunk-${suffix}`;

  afterAll(async () => { if (enabled) await store.close(); });

  test("persists an event graph and supports idempotent replay", async () => {
    await store.ensureSchema();
    const schema = await store.schemaStatus();
    expect(schema.initialized).toBe(true);
    expect(schema.storedVersion).toBe(schema.currentVersion);
    expect(schema.migrations).toContain("v1-initial");
    const first = await store.appendEvent({ id: eventId, kind: "decision", content: `Neo4j integration decision ${suffix}`, source: "test", projectId: suffix });
    const second = await store.appendEvent({ id: eventId, kind: "decision", content: `Neo4j integration decision ${suffix}`, source: "test", projectId: suffix });
    expect(first.id).toBe(eventId);
    expect(second.id).toBe(eventId);
    expect(second.idempotent).toBe(true);
    const keyed = await store.appendEvent({ idempotencyKey: `keyed-event-${suffix}`, kind: "decision", content: `Keyed idempotent event ${suffix}`, projectId: suffix });
    const keyedReplay = await store.appendEvent({ idempotencyKey: `keyed-event-${suffix}`, kind: "decision", content: `Keyed idempotent event ${suffix}`, projectId: suffix });
    expect(keyed.idempotent).toBe(true);
    expect(keyedReplay.id).toBe(keyed.id);
    await expect(store.appendEvent({ id: eventId, kind: "error", content: `Conflicting content ${suffix}`, projectId: suffix })).rejects.toThrow("idempotency conflict");
    const atomicConflictId = `atomic-conflict-${suffix}`;
    const atomicNewId = `atomic-new-${suffix}`;
    await store.appendEvent({ id: atomicConflictId, kind: "decision", content: `Atomic original ${suffix}`, projectId: suffix });
    const beforeAtomic = await store.exportSnapshot();
    const beforeAtomicNode = beforeAtomic.nodes.find((node) => node.labels.includes("Event") && node.properties.id === atomicConflictId);
    await expect(store.appendEvents([
      { id: atomicConflictId, kind: "error", content: `Atomic conflicting update ${suffix}`, projectId: suffix },
      { id: atomicNewId, kind: "decision", content: `Should not be written ${suffix}`, projectId: suffix },
    ])).rejects.toThrow("idempotency conflict");
    const afterAtomic = await store.exportSnapshot();
    const afterAtomicNode = afterAtomic.nodes.find((node) => node.labels.includes("Event") && node.properties.id === atomicConflictId);
    expect(afterAtomic.nodes.some((node) => node.labels.includes("Event") && node.properties.id === atomicNewId)).toBe(false);
    expect(afterAtomicNode?.properties.ingestCount).toBe(beforeAtomicNode?.properties.ingestCount);
    const job = await store.claimNextJob(eventId);
    expect(job?.targetId).toBe(eventId);
    expect((await store.completeJob(job!.id)).status).toBe("completed");
    const leaseEventId = `lease-event-${suffix}`;
    await store.appendEvent({ id: leaseEventId, kind: "tool_result", content: `Lease recovery ${suffix}`, projectId: suffix });
    const previousLease = process.env.JOB_LEASE_SECONDS;
    process.env.JOB_LEASE_SECONDS = "0";
    try {
      const claimed = await store.claimNextJob(leaseEventId);
      const reclaimed = await store.claimNextJob(leaseEventId);
      expect(claimed?.id).toBe(reclaimed?.id);
    expect(Number(reclaimed?.attempts)).toBe(2);
    } finally {
      if (previousLease === undefined) delete process.env.JOB_LEASE_SECONDS;
      else process.env.JOB_LEASE_SECONDS = previousLease;
    }
    await store.appendEvent({ id: `session-two-${suffix}`, kind: "user_message", content: `Cross-session memory ${suffix}`, projectId: suffix, sessionId: "session-two" });
    const crossSession = await store.recall(`Cross-session memory ${suffix}`, { projectId: suffix, includeTestData: true }, 10);
    const sessionScoped = await store.recall(`Cross-session memory ${suffix}`, { projectId: suffix, sessionId: "session-two", includeTestData: true }, 10);
    const otherSession = await store.recall(`Cross-session memory ${suffix}`, { projectId: suffix, sessionId: "session-one", includeTestData: true }, 10);
    expect(crossSession.some((result) => result.id === `session-two-${suffix}`)).toBe(true);
    expect(sessionScoped.some((result) => result.id === `session-two-${suffix}`)).toBe(true);
    expect(otherSession.some((result) => result.id === `session-two-${suffix}`)).toBe(false);
    const snapshot = await store.exportSnapshot();
    expect(snapshot.nodes.some((node) => node.labels.includes("Conversation") && node.properties.id === "session-two")).toBe(true);
    expect(snapshot.nodes.some((node) => node.labels.includes("Message") && node.properties.id === `session-two-${suffix}`)).toBe(true);
    expect(snapshot.nodes.some((node) => node.labels.includes("ReasoningTrace") && node.properties.id === eventId)).toBe(true);
  });

  test("links entity, document, chunk, claim, and memory provenance", async () => {
    await store.upsertEntity({ id: entityId, name: `Integration Project ${suffix}`, type: "project", projectId: suffix });
    await store.upsertDocument({ id: documentId, title: "Integration source", source: "test", content: "Source document", projectId: suffix });
    await store.upsertChunk({ id: chunkId, documentId, content: `Neo4j graph source ${suffix}`, ordinal: 0, entityIds: [entityId], projectId: suffix });
    const claim = await store.createClaim({ statement: `The integration project uses Neo4j ${suffix}`, predicate: "USES", subjectEntityId: entityId, sourceChunkIds: [chunkId], sourceEventIds: [eventId], projectId: suffix });
    const memory = await store.createMemory({ content: `Remember the Neo4j decision ${suffix}`, category: "decision", projectId: suffix, sourceEventIds: [eventId], entityIds: [entityId] });
    const results = await store.recall(`integration decision ${suffix}`, { projectId: suffix, includeTestData: true }, 10);
    const graphResults = await store.recall(`Integration Project ${suffix}`, { projectId: suffix, includeTestData: true }, 10);
    const sourceResults = await store.recall(`Neo4j graph source ${suffix}`, { projectId: suffix, includeTestData: true }, 10);
    expect(memory.status).toBe("active");
    expect(claim.predicate).toBe("USES");
    expect(results.some((result) => result.id === eventId)).toBe(true);
    const recalledMemory = results.find((result) => result.id === memory.id);
    expect(((recalledMemory as Record<string, unknown> | undefined)?.sourceEventIds as string[] | undefined)?.includes(eventId)).toBe(true);
    expect(graphResults.some((result) => result.retrievalMethod === "graph" || (Array.isArray(result.retrievalMethods) && result.retrievalMethods.includes("graph")))).toBe(true);
    expect(sourceResults.some((result) => result.type === "chunk" && result.id === chunkId)).toBe(true);
    expect(sourceResults.some((result) => result.type === "claim" && (result.sourceEventIds as string[] | undefined)?.includes(eventId))).toBe(true);
  });

  test("retrieval method ablations isolate lexical and graph candidates", async () => {
    const ablationEventId = `ablation-event-${suffix}`;
    await store.appendEvent({ id: ablationEventId, kind: "decision", content: `Ablation lexical evidence ${suffix}`, projectId: suffix });
    const lexicalOnly = await store.recall(`Ablation lexical evidence ${suffix}`, { projectId: suffix, includeTestData: true, retrievalMethods: ["lexical"] }, 10);
    const graphOnly = await store.recall(`Ablation lexical evidence ${suffix}`, { projectId: suffix, includeTestData: true, retrievalMethods: ["graph"] }, 10);
    expect(lexicalOnly.some((result) => result.id === ablationEventId && result.retrievalMethod === "lexical")).toBe(true);
    expect(graphOnly.some((result) => result.id === ablationEventId)).toBe(false);
  });

  test("benchmark project data is isolated from global recall", async () => {
    const isolatedId = `benchmark-isolation-${suffix}`;
    const benchmarkProjectId = `benchmark-isolation-project-${suffix}`;
    const previousBenchmarkProjectId = process.env.BENCHMARK_PROJECT_ID;
    process.env.BENCHMARK_PROJECT_ID = benchmarkProjectId;
    try {
      await store.appendEvent({ id: isolatedId, kind: "decision", content: `Benchmark-only isolation marker ${suffix}`, source: "benchmark", projectId: benchmarkProjectId });
      const globalResults = await store.recall(`Benchmark-only isolation marker ${suffix}`, {}, 10);
      const scopedResults = await store.recall(`Benchmark-only isolation marker ${suffix}`, { projectId: benchmarkProjectId }, 10);
      expect(globalResults.some((result) => result.id === isolatedId)).toBe(false);
      expect(scopedResults.some((result) => result.id === isolatedId)).toBe(true);
    } finally {
      if (previousBenchmarkProjectId === undefined) delete process.env.BENCHMARK_PROJECT_ID;
      else process.env.BENCHMARK_PROJECT_ID = previousBenchmarkProjectId;
    }
  });

  test("consolidation worker promotes a queued event into durable knowledge", async () => {
    const workerEventId = `worker-event-${suffix}`;
    await store.appendEvent({ id: workerEventId, kind: "decision", content: `Worker chose Qwen embeddings ${suffix}`, projectId: suffix });
    const worker = new ConsolidationWorker(store, {
      async extract() {
        return {
          memories: [{ content: `The worker chose Qwen embeddings ${suffix}`, category: "decision" as const, confidence: 0.95, entityNames: [`Memory Project ${suffix}`] }],
          entities: [{ name: `Memory Project ${suffix}`, type: "project" as const, aliases: [] }],
          claims: [],
          procedures: [{ title: "Verify memory worker", goal: "Confirm consolidation works", steps: ["Append event", "Run worker"], preconditions: ["Neo4j is reachable"], knownFailures: ["Model endpoint unavailable"], confidence: 0.8 }],
        };
      },
    });
    const result = await worker.processOnce(workerEventId);
    const memories = await store.recall(`worker chose Qwen embeddings ${suffix}`, { projectId: suffix, includeTestData: true }, 10);
    const procedures = await store.recall("model endpoint unavailable", { projectId: suffix, includeTestData: true }, 10);
    expect(result?.error == null).toBe(true);
    expect(memories.some((memory) => memory.kind === "decision")).toBe(true);
    expect(procedures.some((procedure) => procedure.type === "procedure")).toBe(true);
  });

  test("retries transient consolidation failures and then fails bounded jobs", async () => {
    const retryEventId = `retry-event-${suffix}`;
    await store.appendEvent({ id: retryEventId, kind: "error", content: `Retry provider timeout ${suffix}`, projectId: suffix });
    const previousAttempts = process.env.JOB_MAX_ATTEMPTS;
    const previousBackoff = process.env.JOB_RETRY_BACKOFF_SECONDS;
    process.env.JOB_MAX_ATTEMPTS = "2";
    process.env.JOB_RETRY_BACKOFF_SECONDS = "0";
    try {
      const worker = new ConsolidationWorker(store, { async extract() { throw new Error("provider timeout"); } });
      const first = await worker.processOnce(retryEventId);
      const second = await worker.processOnce(retryEventId);
      expect((first as { retry?: { status?: string } }).retry?.status).toBe("queued");
      expect((second as { retry?: { status?: string } }).retry?.status).toBe("failed");
      expect((await store.requeueFailedJob(`consolidate-event:${retryEventId}`))?.status).toBe("queued");
    } finally {
      if (previousAttempts === undefined) delete process.env.JOB_MAX_ATTEMPTS;
      else process.env.JOB_MAX_ATTEMPTS = previousAttempts;
      if (previousBackoff === undefined) delete process.env.JOB_RETRY_BACKOFF_SECONDS;
      else process.env.JOB_RETRY_BACKOFF_SECONDS = previousBackoff;
    }
  });

  test("reconsolidation links conflicting memories without deleting evidence", async () => {
    const first = await store.createMemory({ id: `memory-old-${suffix}`, content: `The integration mode is batch ${suffix}`, category: "fact", subject: `integration-mode-${suffix}`, predicate: "VALUE", object: "batch", projectId: suffix });
    const second = await store.createMemory({ id: `memory-new-${suffix}`, content: `The integration mode is streaming ${suffix}`, category: "fact", subject: `integration-mode-${suffix}`, predicate: "VALUE", object: "streaming", projectId: suffix });
    expect((second as typeof second & { conflicts?: Array<{ id: string; relation: string }> }).conflicts?.some((link) => link.id === first.id && link.relation === "contradicts")).toBe(true);
    const worker = new ConsolidationWorker(store, { async extract() { throw new Error("not used"); } });
    const result = await worker.processOnce(second.id);
    expect(first.status).toBe("active");
    expect(result?.error == null).toBe(true);
    const links = (result as { links?: Array<{ id: string; relation: string }> }).links ?? [];
    expect(links.some((link) => link.id === first.id && link.relation === "contradicts")).toBe(true);
    const otherProject = await store.createMemory({ id: `memory-other-project-${suffix}`, content: `Other project value ${suffix}`, category: "fact", subject: `integration-mode-${suffix}`, predicate: "VALUE", object: "other", projectId: `other-${suffix}` });
    const isolated = await new ConsolidationWorker(store, { async extract() { throw new Error("not used"); } }).processOnce(otherProject.id);
    const isolatedLinks = (isolated as { links?: Array<{ id: string }> }).links ?? [];
    expect(isolatedLinks.some((link) => link.id === first.id)).toBe(false);

    const historical = await store.createMemory({ id: `memory-historical-${suffix}`, content: `The historical integration mode was batch ${suffix}`, category: "fact", subject: `temporal-mode-${suffix}`, predicate: "VALUE", object: "batch", projectId: suffix, validFrom: "2020-01-01T00:00:00.000Z" });
    const current = await store.createMemory({ id: `memory-current-${suffix}`, content: `The current integration mode is streaming ${suffix}`, category: "fact", subject: `temporal-mode-${suffix}`, predicate: "VALUE", object: "streaming", projectId: suffix, validFrom: "2021-01-01T00:00:00.000Z", supersedesMemoryId: historical.id });
    const temporalResults = await store.recall(`historical integration mode ${suffix}`, { projectId: suffix, includeHistorical: true, includeTestData: true }, 20);
    expect(temporalResults.some((result) => result.id === historical.id)).toBe(true);
    expect(current.status).toBe("active");
    const supersededReview = await store.listMemories({ projectId: suffix }, 50, "superseded") as Array<{ id?: string; supersededByIds?: string[] }>;
    const historicalReview = supersededReview.find((memory) => memory.id === historical.id);
    expect(historicalReview?.supersededByIds?.includes(current.id)).toBe(true);
  });

  test("retraction hides a memory while preserving its node", async () => {
    const memoryId = `memory-retract-${suffix}`;
    await store.createMemory({ id: memoryId, content: `Sensitive memory to retract ${suffix}`, category: "working", projectId: suffix });
    const retracted = await store.retractMemory(memoryId, "test cleanup");
    const results = await store.recall(`Sensitive memory to retract ${suffix}`, { projectId: suffix, includeTestData: true }, 10);
    expect(retracted?.status).toBe("retracted");
    expect(results.some((result) => result.id === memoryId)).toBe(false);
  });

  test("auto-generated entities remain isolated by project scope", async () => {
    const first = await store.upsertEntity({ name: `Shared Entity ${suffix}`, type: "project", projectId: suffix });
    const second = await store.upsertEntity({ name: `Shared Entity ${suffix}`, type: "project", projectId: `other-${suffix}` });
    expect(first.id).not.toBe(second.id);
  });

  test("tracks memory utility, alias identity, and context hubs", async () => {
    const canonical = await store.upsertEntity({ id: `canonical-${suffix}`, name: `Qwen Embedding ${suffix}`, type: "technology", aliases: [`Qwen3 ${suffix}`], projectId: suffix });
    const alias = await store.upsertEntity({ id: `alias-${suffix}`, name: `Qwen3 ${suffix}`, type: "technology", projectId: suffix });
    expect(canonical.id).not.toBe(alias.id);
    const utilityMemory = await store.createMemory({ id: `utility-memory-${suffix}`, content: `Useful memory ${suffix}`, category: "fact", projectId: suffix, entityIds: [canonical.id] });
    const utility = await store.recordMemoryUtility(utilityMemory.id!, true, "used in answer");
    expect(utility?.utilityScore).toBe(1);
    const hubs = await store.contextHubs(10, { projectId: suffix, includeTestData: true });
    expect(hubs.hubs.some((hub) => hub.id === canonical.id || hub.id === utilityMemory.id)).toBe(true);
    const archived = await store.archiveMemory(utilityMemory.id!, "no longer current");
    expect(archived?.status).toBe("archived");
    const archivedResults = await store.recall(`Useful memory ${suffix}`, { projectId: suffix, includeHistorical: true, includeTestData: true }, 10);
    expect(archivedResults.some((result) => result.id === utilityMemory.id)).toBe(false);
    expect((await store.recall(`Useful memory ${suffix}`, { projectId: suffix, includeTestData: true }, 10)).some((result) => result.id === utilityMemory.id)).toBe(false);
    expect((await store.getEvent(eventId))?.id).toBe(eventId);
  });

  test("automatically schedules and persists a session reflection", async () => {
    const previousDelay = process.env.REFLECTION_DELAY_SECONDS;
    process.env.REFLECTION_DELAY_SECONDS = "0";
    const sessionId = `reflection-session-${suffix}`;
    try {
      await store.appendEvents([
        { id: `reflection-user-${suffix}`, kind: "user_message", content: `The user asked about reflection ${suffix}`, projectId: suffix, sessionId },
        { id: `reflection-error-${suffix}`, kind: "error", content: `The first attempt failed ${suffix}`, projectId: suffix, sessionId },
      ]);
      const worker = new ConsolidationWorker(store, {
        async extract() { return { memories: [], entities: [], claims: [], procedures: [] }; },
        async reflect() { return { reflection: `Reflection for ${suffix}`, lessons: [`Lesson from ${suffix}`], failures: [`Failure lesson ${suffix}`] }; },
      });
      const result = await worker.processOnce(sessionId);
      expect(result?.error == null).toBe(true);
      const memories = await store.recall(`Reflection for ${suffix}`, { projectId: suffix, sessionId, includeTestData: true }, 10);
      expect(memories.some((memory) => memory.kind === "reflection")).toBe(true);
    } finally {
      if (previousDelay === undefined) delete process.env.REFLECTION_DELAY_SECONDS;
      else process.env.REFLECTION_DELAY_SECONDS = previousDelay;
    }
  });
});
