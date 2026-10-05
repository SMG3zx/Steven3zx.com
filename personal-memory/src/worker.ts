import { OpenAICompatibleConsolidator, createConsolidator, parseExtraction, type Consolidator } from "./consolidation";
import { Neo4jStore } from "./neo4j-store";
import type { CuratedMemory, JobKind, MemoryEvent } from "./types";

export function failureMemoryFromEvent(event: MemoryEvent): CuratedMemory | null {
  const metadata = event.metadata ?? {};
  const status = String(metadata.status ?? metadata.success ?? "").toLowerCase();
  const looksLikeFailure = event.kind === "error"
    || status === "failed"
    || status === "error"
    || status === "false"
    || /\b(error|failed|failure|exception|timeout|timed out|could not|unable to)\b/i.test(event.content);
  if (!looksLikeFailure) return null;
  return {
    content: `Failure feedback from ${event.kind}: ${event.content}`,
    category: "episode",
    confidence: event.kind === "error" ? 0.85 : 0.7,
    metadata: { memoryKind: "reflexion_failure", eventKind: event.kind, originalMetadata: metadata },
    sourceEventIds: event.id ? [event.id] : [],
    userId: event.userId,
    projectId: event.projectId,
    sessionId: event.sessionId,
  };
}

export class ConsolidationWorker {
  constructor(private readonly store: Neo4jStore, private readonly consolidator: Consolidator) {}

  async processOnce(targetId?: string) {
    // Keep extraction jobs queued when no model endpoint is configured. This
    // avoids repeatedly leasing and retrying thousands of durable events in a
    // deployment that intentionally runs retrieval-only or is being bootstrapped.
    const skipKinds: JobKind[] = !process.env.MODEL_BASE_URL && this.consolidator instanceof OpenAICompatibleConsolidator
      ? ["consolidate_event", "reflect_session"]
      : [];
    const job = await this.store.claimNextJob(targetId, skipKinds, process.env.CONSOLIDATION_REQUIRE_APPROVAL === "1");
    if (!job) return null;
    try {
      if (job.kind === "reconsolidate_memory") {
        const links = await this.store.reconsolidateMemory(job.targetId);
        await this.store.completeJob(job.id);
        return { ...job, links };
      }
      if (job.kind === "reflect_session") {
        const events = await this.store.getSessionEvents(job.targetId);
        if (!events.length) {
          await this.store.completeJob(job.id);
          return { ...job, skipped: true, reason: "session has no events" };
        }
        const reflection = this.consolidator.reflect
          ? await this.consolidator.reflect(events)
          : {
              reflection: `Session contained ${events.length} event(s); durable reflection requires a configured model provider.`,
              lessons: [],
              failures: [],
            };
        const sourceEventIds = events.flatMap((event) => event.id ? [event.id] : []);
        const session = events.find((event) => event.sessionId === job.targetId);
        const memories = [
          { content: reflection.reflection, category: "reflection" as const },
          ...reflection.lessons.map((content) => ({ content, category: "belief" as const })),
          ...reflection.failures.map((content) => ({ content, category: "failure" as const })),
        ];
        const saved = [];
        for (const memory of memories) saved.push(await this.store.createMemory({ ...memory, sourceEventIds, userId: session?.userId, projectId: session?.projectId, sessionId: job.targetId, confidence: memory.category === "failure" ? 0.85 : 0.8 }));
        await this.store.completeJob(job.id);
        return { ...job, reflection, memories: saved };
      }
      if (job.kind !== "consolidate_event") {
        await this.store.completeJob(job.id);
        return { ...job, skipped: true };
      }
      const event = await this.store.getEvent(job.targetId);
      if (!event) throw new Error(`event ${job.targetId} was not found`);
      const failureMemory = failureMemoryFromEvent(event);
      if (failureMemory) await this.store.createMemory(failureMemory);
      const extraction = job.proposal ? parseExtraction(JSON.parse(job.proposal)) : await this.consolidator.extract(event);
      const entityIds = new Map<string, string>();
      for (const entity of extraction.entities) {
        const saved = await this.store.upsertEntity({ ...entity, userId: event.userId, projectId: event.projectId, sessionId: event.sessionId });
        entityIds.set(normalize(entity.name), saved.id);
        for (const alias of entity.aliases) entityIds.set(normalize(alias), saved.id);
      }
      for (const memory of extraction.memories) {
        await this.store.createMemory({
          ...memory,
          userId: event.userId,
          projectId: event.projectId,
          sessionId: event.sessionId,
          sourceEventIds: [event.id!],
          entityIds: memory.entityNames.flatMap((name) => entityIds.get(normalize(name)) ?? []),
        });
      }
      for (const claim of extraction.claims) {
        const subjectEntityId = entityIds.get(normalize(claim.subjectName));
        if (!subjectEntityId) continue;
        await this.store.createClaim({
          statement: claim.statement,
          predicate: claim.predicate,
          subjectEntityId,
          objectEntityId: claim.objectName ? entityIds.get(normalize(claim.objectName)) : undefined,
          confidence: claim.confidence,
          userId: event.userId,
          projectId: event.projectId,
          sessionId: event.sessionId,
          sourceEventIds: [event.id!],
        });
      }
      for (const procedure of extraction.procedures) {
        await this.store.upsertProcedure({ ...procedure, userId: event.userId, projectId: event.projectId, sessionId: event.sessionId, sourceEventIds: [event.id!] });
      }
      await this.store.completeJob(job.id);
      return { ...job, extracted: extraction };
    } catch (error) {
      const message = error instanceof Error ? error.message : "unknown consolidation error";
      const retry = await this.store.retryJob(job.id, message);
      return { ...job, error: message, retry };
    }
  }
}

function normalize(value: string) {
  return value.trim().toLowerCase().replace(/\s+/g, " ");
}

if (import.meta.main) {
  const store = new Neo4jStore();
  const worker = new ConsolidationWorker(store, createConsolidator());
  await store.ensureSchema();
  const pollMs = Number(process.env.WORKER_POLL_MS ?? 1000);
  let stopping = false;
  const shutdown = () => { stopping = true; };
  process.once("SIGINT", shutdown);
  process.once("SIGTERM", shutdown);
  console.log("Personal Memory consolidation worker started");
  while (!stopping) {
    const result = await worker.processOnce();
    if (result) console.log(JSON.stringify(result));
    if (!result && !stopping) await new Promise((resolve) => setTimeout(resolve, pollMs));
  }
  await store.close();
  console.log("Personal Memory consolidation worker stopped");
}
