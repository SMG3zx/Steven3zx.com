import neo4j, { type Driver, type Record as Neo4jRecord } from "neo4j-driver";
import { EmbeddingClient } from "./embeddings";
import { RerankerClient } from "./reranker";
import { memoryRetentionScore, retrievalPlan } from "./retrieval";
import type { RetrievalRoute } from "./retrieval";
import { reciprocalRankFuse } from "./rank-fusion";
import type { ClaimInput, ChunkInput, CuratedMemory, DocumentInput, Entity, JobKind, MemoryEvent, MemoryJob, MemoryScope, MemorySnapshot, ProcedureInput } from "./types";
import { validateContent } from "./content-limits";

export const SCHEMA_VERSION = 2;
const SCHEMA_MIGRATION_ID = "v2-content-index-safety";

export class Neo4jStore {
  private readonly driver: Driver;
  private readonly database: string;
  private readonly embeddings = new EmbeddingClient();
  private readonly reranker = new RerankerClient();

  constructor() {
    const uri = process.env.NEO4J_URI;
    const username = process.env.NEO4J_USERNAME;
    const password = process.env.NEO4J_PASSWORD;
    if (!uri || !username || !password) throw new Error("NEO4J_URI, NEO4J_USERNAME, and NEO4J_PASSWORD are required");
    this.driver = neo4j.driver(uri, neo4j.auth.basic(username, password));
    this.database = process.env.NEO4J_DATABASE ?? "neo4j";
  }

  async close() {
    await this.driver.close();
  }

  async verify() {
    await this.driver.verifyConnectivity();
    return true;
  }

  async exportSnapshot(): Promise<MemorySnapshot> {
    const session = this.driver.session({ database: this.database });
    try {
      const nodes = await session.run("MATCH (n) RETURN elementId(n) AS key, labels(n) AS labels, properties(n) AS properties");
      const relationships = await session.run("MATCH (source)-[r]->(target) RETURN elementId(source) AS sourceKey, type(r) AS type, elementId(target) AS targetKey, properties(r) AS properties");
      return {
        format: "personal-memory-snapshot",
        version: 1,
        exportedAt: new Date().toISOString(),
        nodes: nodes.records.map((record) => ({ key: String(record.get("key")), labels: record.get("labels") as string[], properties: serializeProperties(record.get("properties") as Record<string, unknown>) })),
        relationships: relationships.records.map((record) => ({ sourceKey: String(record.get("sourceKey")), type: String(record.get("type")), targetKey: String(record.get("targetKey")), properties: serializeProperties(record.get("properties") as Record<string, unknown>) })),
      };
    } finally {
      await session.close();
    }
  }

  async stats() {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `CALL {
           MATCH (n) RETURN count(n) AS nodeCount
         }
         CALL {
           MATCH ()-[r]->() RETURN count(r) AS relationshipCount
         }
         CALL {
           MATCH (j:Job) RETURN collect({status: j.status, count: 1}) AS rawJobs
         }
         RETURN nodeCount, relationshipCount, rawJobs`,
      );
      if (!result.records.length) return { nodeCount: 0, relationshipCount: 0, jobs: {} };
      const value = toObject(result.records[0]);
      const jobs: Record<string, number> = {};
      for (const job of (value.rawJobs as Array<{ status?: string }>) ?? []) {
        const status = job.status ?? "unknown";
        jobs[status] = (jobs[status] ?? 0) + 1;
      }
      return { nodeCount: Number(value.nodeCount), relationshipCount: Number(value.relationshipCount), jobs };
    } finally {
      await session.close();
    }
  }

  async schemaStatus() {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (s:SchemaMetadata {name: 'personal-memory'})
         RETURN s.version AS version, s.createdAt AS createdAt, s.checkedAt AS checkedAt,
                coalesce(s.appliedMigrations, []) AS appliedMigrations`,
      );
      if (!result.records.length) return { currentVersion: SCHEMA_VERSION, storedVersion: null, initialized: false, migrations: [] };
      const record = result.records[0];
      return {
        currentVersion: SCHEMA_VERSION,
        storedVersion: Number(record.get("version")),
        initialized: true,
        createdAt: String(record.get("createdAt") ?? ""),
        checkedAt: String(record.get("checkedAt") ?? ""),
        migrations: (record.get("appliedMigrations") as unknown[]).map(String),
      };
    } finally {
      await session.close();
    }
  }

  /** Use GDS PageRank when a projection exists, with a safe Cypher fallback. */
  async contextHubs(limit = 20, scope: MemoryScope = {}) {
    const session = this.driver.session({ database: this.database });
    const graphName = process.env.NEO4J_GDS_GRAPH ?? "personal-memory-context";
    try {
      try {
        const exists = await session.run("CALL gds.graph.exists($graphName) YIELD exists RETURN exists", { graphName });
        if (exists.records.length && Boolean(exists.records[0].get("exists"))) {
          const pageRank = await session.run(
            `CALL gds.pageRank.stream($graphName)
             YIELD nodeId, score
             WITH gds.util.asNode(nodeId) AS node, score
             WHERE node:Entity
               AND ($userId IS NULL OR node.userId IS NULL OR node.userId = $userId)
               AND ($projectId IS NULL OR node.projectId IS NULL OR node.projectId = $projectId)
               AND ($sessionId IS NULL OR node.sessionId IS NULL OR node.sessionId = $sessionId)
               AND ($includeTestData OR coalesce(node.source, '') <> 'test')
             RETURN node.id AS id, node.name AS label, node.type AS type, score
             ORDER BY score DESC LIMIT $limit`,
            { graphName, userId: scope.userId ?? null, projectId: scope.projectId ?? null, sessionId: scope.sessionId ?? null, includeTestData: scope.includeTestData ?? false, limit: neo4j.int(Math.min(Math.max(limit, 1), 100)) },
          );
          return { method: "gds-pagerank", graphName, hubs: pageRank.records.map((record) => serializeProperties(toObject(record))) };
        }
      } catch {
        // GDS is optional; continue with the portable degree-based fallback.
      }
      const result = await session.run(
        `MATCH (n)
         WHERE (n:Entity OR n:Memory OR n:Claim OR n:Procedure)
           AND ($userId IS NULL OR n.userId = $userId)
           AND ($projectId IS NULL OR n.projectId = $projectId)
           AND ($sessionId IS NULL OR n.sessionId = $sessionId)
           AND ($includeTestData OR coalesce(n.source, '') <> 'test')
         OPTIONAL MATCH (n)-[r]-()
         RETURN labels(n)[0] AS type, n.id AS id, coalesce(n.name, n.title, n.content, n.statement) AS label,
                count(r) AS degree
         ORDER BY degree DESC, label ASC LIMIT $limit`,
        { userId: scope.userId ?? null, projectId: scope.projectId ?? null, sessionId: scope.sessionId ?? null, includeTestData: scope.includeTestData ?? false, limit: neo4j.int(Math.min(Math.max(limit, 1), 100)) },
      );
      return { method: "cypher-degree", graphName, hubs: result.records.map((record) => serializeProperties(toObject(record))) };
    } finally {
      await session.close();
    }
  }

  async ensureSchema() {
    const session = this.driver.session({ database: this.database });
    try {
      await session.run("CREATE CONSTRAINT event_id IF NOT EXISTS FOR (e:Event) REQUIRE e.id IS UNIQUE");
      await session.run("CREATE CONSTRAINT memory_id IF NOT EXISTS FOR (m:Memory) REQUIRE m.id IS UNIQUE");
      await session.run("CREATE CONSTRAINT entity_id IF NOT EXISTS FOR (e:Entity) REQUIRE e.id IS UNIQUE");
      await session.run("CREATE CONSTRAINT document_id IF NOT EXISTS FOR (d:Document) REQUIRE d.id IS UNIQUE");
      await session.run("CREATE CONSTRAINT chunk_id IF NOT EXISTS FOR (c:Chunk) REQUIRE c.id IS UNIQUE");
      await session.run("CREATE CONSTRAINT claim_id IF NOT EXISTS FOR (c:Claim) REQUIRE c.id IS UNIQUE");
      await session.run("CREATE CONSTRAINT procedure_id IF NOT EXISTS FOR (p:Procedure) REQUIRE p.id IS UNIQUE");
      await session.run("CREATE CONSTRAINT job_id IF NOT EXISTS FOR (j:Job) REQUIRE j.id IS UNIQUE");
      await session.run("CREATE CONSTRAINT conversation_id IF NOT EXISTS FOR (c:Conversation) REQUIRE c.id IS UNIQUE");
      await session.run("CREATE CONSTRAINT message_id IF NOT EXISTS FOR (m:Message) REQUIRE m.id IS UNIQUE");
      await session.run("CREATE CONSTRAINT reasoning_trace_id IF NOT EXISTS FOR (r:ReasoningTrace) REQUIRE r.id IS UNIQUE");
      // RANGE indexes cannot accept the large text values used by events and memories.
      // DROP IF EXISTS repairs databases created by schema version 1 without manual intervention.
      await session.run("DROP INDEX event_content IF EXISTS");
      await session.run("DROP INDEX memory_content IF EXISTS");
      await session.run("CREATE INDEX entity_name IF NOT EXISTS FOR (e:Entity) ON (e.name)");
      await session.run("CREATE INDEX event_occurred_at IF NOT EXISTS FOR (e:Event) ON (e.occurredAt)");
      await session.run("CREATE INDEX event_scope IF NOT EXISTS FOR (e:Event) ON (e.userId, e.projectId, e.sessionId)");
      await session.run("CREATE INDEX conversation_session IF NOT EXISTS FOR (c:Conversation) ON (c.sessionId)");
      await session.run("CREATE INDEX message_occurred_at IF NOT EXISTS FOR (m:Message) ON (m.occurredAt)");
      await session.run("CREATE INDEX memory_validity IF NOT EXISTS FOR (m:Memory) ON (m.validFrom, m.validUntil)");
      await session.run("CREATE INDEX memory_scope IF NOT EXISTS FOR (m:Memory) ON (m.userId, m.projectId, m.sessionId)");
      await session.run("CREATE INDEX chunk_scope IF NOT EXISTS FOR (c:Chunk) ON (c.userId, c.projectId, c.sessionId)");
      await session.run("CREATE INDEX claim_scope IF NOT EXISTS FOR (c:Claim) ON (c.userId, c.projectId, c.sessionId)");
      await session.run("CREATE INDEX procedure_scope IF NOT EXISTS FOR (p:Procedure) ON (p.userId, p.projectId, p.sessionId)");
      await session.run("CREATE FULLTEXT INDEX event_fulltext IF NOT EXISTS FOR (e:Event) ON EACH [e.content]");
      await session.run("CREATE FULLTEXT INDEX memory_fulltext IF NOT EXISTS FOR (m:Memory) ON EACH [m.content]");
      await session.run("CREATE FULLTEXT INDEX procedure_fulltext IF NOT EXISTS FOR (p:Procedure) ON EACH [p.title, p.goal]");
      await session.run("CREATE FULLTEXT INDEX procedure_fulltext_v2 IF NOT EXISTS FOR (p:Procedure) ON EACH [p.title, p.goal, p.steps, p.preconditions, p.knownFailures]");
      await session.run("CREATE FULLTEXT INDEX claim_fulltext IF NOT EXISTS FOR (c:Claim) ON EACH [c.statement, c.predicate]");
      await session.run("CREATE FULLTEXT INDEX chunk_fulltext IF NOT EXISTS FOR (c:Chunk) ON EACH [c.content]");
      const dimensions = Number(process.env.EMBEDDING_DIMENSIONS ?? 1024);
      if (!Number.isInteger(dimensions) || dimensions < 32 || dimensions > 4096) throw new Error("EMBEDDING_DIMENSIONS must be an integer between 32 and 4096");
      await session.run(`CREATE VECTOR INDEX memory_embedding IF NOT EXISTS FOR (m:Memory) ON m.embedding OPTIONS { indexConfig: { \`vector.dimensions\`: ${dimensions}, \`vector.similarity_function\`: 'cosine' } }`);
      await session.run(`CREATE VECTOR INDEX chunk_embedding IF NOT EXISTS FOR (c:Chunk) ON c.embedding OPTIONS { indexConfig: { \`vector.dimensions\`: ${dimensions}, \`vector.similarity_function\`: 'cosine' } }`);
      const schemaResult = await session.run(
        `MERGE (s:SchemaMetadata {name: 'personal-memory'})
         ON CREATE SET s.version = $version, s.createdAt = datetime(), s.appliedMigrations = [$migration]
         SET s.checkedAt = datetime()
         RETURN s.version AS version`,
        { version: neo4j.int(SCHEMA_VERSION), migration: SCHEMA_MIGRATION_ID },
      );
      const storedVersion = Number(schemaResult.records[0]?.get("version") ?? 0);
      if (storedVersion > SCHEMA_VERSION) throw new Error(`database schema version ${storedVersion} is newer than supported version ${SCHEMA_VERSION}`);
      if (storedVersion < SCHEMA_VERSION) {
        await session.run(
          `MATCH (s:SchemaMetadata {name: 'personal-memory'})
           SET s.version = $version,
               s.checkedAt = datetime(),
               s.appliedMigrations = coalesce(s.appliedMigrations, []) + $migration`,
          { version: neo4j.int(SCHEMA_VERSION), migration: SCHEMA_MIGRATION_ID },
        );
      }
    } finally {
      await session.close();
    }
  }

  async appendEvent(input: MemoryEvent) {
    return (await this.appendEvents([input]))[0];
  }

  async appendEvents(inputs: MemoryEvent[]) {
    for (const input of inputs) validateContent(input.content);
    const events = inputs.map((input) => ({
      id: input.id ?? input.idempotencyKey ?? crypto.randomUUID(),
      kind: input.kind,
      content: input.content,
      source: input.source ?? "unknown",
      metadata: JSON.stringify(input.metadata ?? {}),
      occurredAt: input.occurredAt ?? new Date().toISOString(),
      userId: input.userId ?? null,
      projectId: input.projectId ?? null,
      sessionId: input.sessionId ?? null,
    }));
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.executeWrite(async (tx) => {
        const existing = await tx.run(
          `UNWIND $events AS event
           OPTIONAL MATCH (e:Event {id: event.id})
           RETURN event.id AS id, event.content AS incomingContent, event.kind AS incomingKind,
                  e.content AS storedContent, e.kind AS storedKind`,
          { events },
        );
        for (const record of existing.records) {
          const storedContent = record.get("storedContent");
          const storedKind = record.get("storedKind");
          if (storedContent !== null && (storedContent !== record.get("incomingContent") || storedKind !== record.get("incomingKind"))) {
            throw new Error(`idempotency conflict for event ${String(record.get("id"))}`);
          }
        }
        return tx.run(
        `UNWIND $events AS event
         MERGE (e:Event {id: event.id})
         ON CREATE SET e += event, e.ingestCount = 1
         ON MATCH SET e.ingestCount = coalesce(e.ingestCount, 0) + 1
         FOREACH (id IN CASE WHEN e.userId IS NULL THEN [] ELSE [e.userId] END |
           MERGE (u:User {id: id}) MERGE (u)-[:GENERATED]->(e))
         FOREACH (id IN CASE WHEN e.projectId IS NULL THEN [] ELSE [e.projectId] END |
           MERGE (p:Project {id: id}) MERGE (p)-[:CONTAINS_EVENT]->(e))
         FOREACH (_ IN CASE WHEN e.sessionId IS NULL THEN [] ELSE [1] END |
           MERGE (c:Conversation {id: e.sessionId})
           ON CREATE SET c.sessionId = e.sessionId, c.createdAt = e.occurredAt
           SET c.updatedAt = e.occurredAt, c.userId = e.userId, c.projectId = e.projectId
           MERGE (m:Message {id: e.id})
           ON CREATE SET m.role = CASE e.kind WHEN 'user_message' THEN 'user' WHEN 'tool_call' THEN 'tool' WHEN 'tool_result' THEN 'tool' ELSE 'assistant' END,
                         m.kind = e.kind, m.content = e.content, m.occurredAt = e.occurredAt,
                         m.userId = e.userId, m.projectId = e.projectId, m.sessionId = e.sessionId
           MERGE (c)-[:HAS_MESSAGE]->(m)
           MERGE (m)-[:REPRESENTS]->(e))
         FOREACH (_ IN CASE WHEN e.kind = 'decision' THEN [1] ELSE [] END |
           MERGE (r:ReasoningTrace {id: e.id})
           ON CREATE SET r.content = e.content, r.occurredAt = e.occurredAt, r.sessionId = e.sessionId,
                         r.userId = e.userId, r.projectId = e.projectId
           MERGE (r)-[:DERIVED_FROM]->(e))
         MERGE (j:Job {id: 'consolidate-event:' + e.id})
         ON CREATE SET j.kind = 'consolidate_event', j.targetId = e.id, j.status = 'queued',
                       j.approvalStatus = $approvalStatus, j.attempts = 0, j.availableAt = e.occurredAt, j.createdAt = datetime()
         FOREACH (_ IN CASE WHEN e.sessionId IS NULL THEN [] ELSE [1] END |
           MERGE (rj:Job {id: 'reflect-session:' + e.sessionId})
           ON CREATE SET rj.kind = 'reflect_session', rj.targetId = e.sessionId, rj.attempts = 0, rj.createdAt = datetime(), rj.approvalStatus = $approvalStatus
           SET rj.status = 'queued', rj.availableAt = datetime() + duration({seconds: $reflectionDelaySeconds}))
         RETURN e.id AS id, e.content AS storedContent, e.kind AS storedKind`,
        { events, reflectionDelaySeconds: neo4j.int(Number(process.env.REFLECTION_DELAY_SECONDS ?? 300)), approvalStatus: process.env.CONSOLIDATION_REQUIRE_APPROVAL === "1" ? "pending" : "approved" },
        );
      });
      const storedById = new Map(result.records.map((record) => [String(record.get("id")), record]));
      for (const event of events) {
        const stored = storedById.get(event.id);
        if (stored && (stored.get("storedContent") !== event.content || stored.get("storedKind") !== event.kind)) {
          throw new Error(`idempotency conflict for event ${event.id}`);
        }
      }
      return events.map((event, index) => ({ ...event, idempotent: Boolean(inputs[index].id || inputs[index].idempotencyKey) }));
    } finally {
      await session.close();
    }
  }

  async getEvent(id: string, scope: MemoryScope = {}): Promise<MemoryEvent | null> {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (e:Event {id: $id})
         WHERE ($userId IS NULL OR e.userId = $userId)
           AND ($projectId IS NULL OR e.projectId = $projectId)
           AND ($sessionId IS NULL OR e.sessionId = $sessionId)
         RETURN e.id AS id, e.kind AS kind, e.content AS content, e.source AS source,
                e.metadata AS metadata, e.occurredAt AS occurredAt, e.userId AS userId,
                e.projectId AS projectId, e.sessionId AS sessionId`,
        { id, userId: scope.userId ?? null, projectId: scope.projectId ?? null, sessionId: scope.sessionId ?? null },
      );
      if (!result.records.length) return null;
      const value = toObject(result.records[0]);
      let metadata: Record<string, unknown> | undefined;
      try { metadata = value.metadata ? JSON.parse(String(value.metadata)) : undefined; } catch { metadata = undefined; }
      return { ...value, metadata } as unknown as MemoryEvent;
    } finally {
      await session.close();
    }
  }

  async getSessionEvents(sessionId: string, limit = 200): Promise<MemoryEvent[]> {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (e:Event {sessionId: $sessionId})
         RETURN e.id AS id, e.kind AS kind, e.content AS content, e.source AS source,
                e.metadata AS metadata, e.occurredAt AS occurredAt, e.userId AS userId,
                e.projectId AS projectId, e.sessionId AS sessionId
         ORDER BY datetime(e.occurredAt) ASC, e.id ASC LIMIT $limit`,
        { sessionId, limit: neo4j.int(Math.min(Math.max(limit, 1), 1000)) },
      );
      return result.records.map((record) => {
        const value = toObject(record);
        try { value.metadata = value.metadata ? JSON.parse(String(value.metadata)) : undefined; } catch { value.metadata = undefined; }
        return value as unknown as MemoryEvent;
      });
    } finally {
      await session.close();
    }
  }

  async claimNextJob(targetId?: string, skipKinds: JobKind[] = [], requireApproval = false): Promise<MemoryJob | null> {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.executeWrite((tx) => tx.run(
        `CALL {
           MATCH (j:Job)
           WHERE ($targetId IS NULL OR j.targetId = $targetId)
             AND ((j.status = 'queued' AND (j.availableAt IS NULL OR datetime(j.availableAt) <= datetime()))
                OR (j.status = 'running' AND j.leaseUntil IS NOT NULL AND datetime(j.leaseUntil) <= datetime()))
             AND NOT j.kind IN $skipKinds
             AND ($requireApproval = false OR coalesce(j.approvalStatus, 'approved') = 'approved')
           WITH j ORDER BY j.availableAt ASC, j.createdAt ASC LIMIT 1
           SET j.status = 'running', j.attempts = coalesce(j.attempts, 0) + 1, j.startedAt = datetime(),
               j.leaseUntil = datetime() + duration({seconds: $leaseSeconds})
           RETURN j
         }
         RETURN j.id AS id, j.kind AS kind, j.targetId AS targetId,
                j.status AS status, j.availableAt AS availableAt, j.attempts AS attempts,
                j.leaseUntil AS leaseUntil, j.error AS error, j.approvalStatus AS approvalStatus, j.proposal AS proposal`,
         { targetId: targetId ?? null, skipKinds, requireApproval, leaseSeconds: neo4j.int(Number(process.env.JOB_LEASE_SECONDS ?? 300)) },
      ));
      return result.records.length ? toObject(result.records[0]) as unknown as MemoryJob : null;
    } finally {
      await session.close();
    }
  }

  async listJobs(status?: string, limit = 100) {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (j:Job)
         OPTIONAL MATCH (e:Event {id: j.targetId})
         WHERE $status IS NULL OR j.status = $status
         RETURN j.id AS id, j.kind AS kind, j.targetId AS targetId, j.status AS status, j.approvalStatus AS approvalStatus, j.proposal AS proposal,
                j.availableAt AS availableAt, j.attempts AS attempts, j.leaseUntil AS leaseUntil,
                j.error AS error, j.createdAt AS createdAt, j.startedAt AS startedAt,
                j.completedAt AS completedAt, e.kind AS eventKind, e.source AS eventSource,
                e.content AS eventContent, e.occurredAt AS eventOccurredAt
         ORDER BY coalesce(j.createdAt, j.availableAt) DESC
         LIMIT $limit`,
        { status: status ?? null, limit: neo4j.int(Math.min(Math.max(limit, 1), 200)) },
      );
      return result.records.map((record) => serializeProperties(toObject(record)));
    } finally {
      await session.close();
    }
  }

  async approveJob(id: string) {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (j:Job {id: $id})
         SET j.approvalStatus = 'approved', j.status = CASE WHEN j.status = 'failed' AND j.approvalStatus = 'rejected' THEN 'queued' ELSE j.status END,
             j.availableAt = datetime(), j.rejectedAt = null
         RETURN j.id AS id, j.status AS status, j.approvalStatus AS approvalStatus`,
        { id },
      );
      return result.records.length ? toObject(result.records[0]) : null;
    } finally { await session.close(); }
  }

  async getJobEvent(id: string) {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (j:Job {id: $id}) OPTIONAL MATCH (e:Event {id: j.targetId})
         RETURN j.id AS id, j.kind AS kind, j.targetId AS targetId, j.status AS status,
                j.approvalStatus AS approvalStatus, j.proposal AS proposal,
                e.id AS eventId, e.kind AS eventKind, e.content AS content, e.source AS source,
                e.metadata AS metadata, e.occurredAt AS occurredAt, e.userId AS userId,
                e.projectId AS projectId, e.sessionId AS sessionId`,
        { id },
      );
      if (!result.records.length) return null;
      const value = toObject(result.records[0]);
      let metadata: Record<string, unknown> | undefined;
      try { metadata = value.metadata ? JSON.parse(String(value.metadata)) : undefined; } catch { metadata = undefined; }
      return { ...value, event: value.eventId ? { id: value.eventId, kind: value.eventKind, content: value.content, source: value.source, metadata, occurredAt: value.occurredAt, userId: value.userId, projectId: value.projectId, sessionId: value.sessionId } : null };
    } finally { await session.close(); }
  }

  async saveJobProposal(id: string, proposal: unknown) {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (j:Job {id: $id}) SET j.proposal = $proposal, j.proposalAt = datetime()
         RETURN j.id AS id, j.approvalStatus AS approvalStatus, j.proposal AS proposal`,
        { id, proposal: JSON.stringify(proposal) },
      );
      return result.records.length ? toObject(result.records[0]) : null;
    } finally { await session.close(); }
  }

  async rejectJob(id: string, reason?: string) {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (j:Job {id: $id})
         SET j.approvalStatus = 'rejected', j.status = 'failed', j.error = $reason, j.rejectedAt = datetime(), j.leaseUntil = null
         RETURN j.id AS id, j.status AS status, j.approvalStatus AS approvalStatus, j.error AS error`,
        { id, reason: reason ?? "rejected during review" },
      );
      return result.records.length ? toObject(result.records[0]) : null;
    } finally { await session.close(); }
  }

  async completeJob(id: string, error?: string) {
    const session = this.driver.session({ database: this.database });
    try {
      const status = error ? "failed" : "completed";
      await session.run(
        `MATCH (j:Job {id: $id})
         SET j.status = $status, j.completedAt = datetime(), j.error = $error`,
        { id, status, error: error ?? null },
      );
      return { id, status };
    } finally {
      await session.close();
    }
  }

  async retryJob(id: string, error: string, maxAttempts = Number(process.env.JOB_MAX_ATTEMPTS ?? 5)) {
    const session = this.driver.session({ database: this.database });
    try {
      const current = await session.run("MATCH (j:Job {id: $id}) RETURN coalesce(j.attempts, 0) AS attempts", { id });
      if (!current.records.length) return { status: "missing" };
      const attempts = Number(current.records[0].get("attempts"));
      const backoff = Number(process.env.JOB_RETRY_BACKOFF_SECONDS ?? 5) * 2 ** Math.max(0, attempts - 1);
      const result = await session.run(
        `MATCH (j:Job {id: $id})
         WITH j, coalesce(j.attempts, 0) AS attempts
         SET j.error = $error,
             j.status = CASE WHEN attempts < $maxAttempts THEN 'queued' ELSE 'failed' END,
             j.availableAt = CASE WHEN attempts < $maxAttempts THEN datetime() + duration({seconds: $backoff}) ELSE j.availableAt END,
             j.leaseUntil = null,
             j.completedAt = CASE WHEN attempts < $maxAttempts THEN null ELSE datetime() END
         RETURN j.status AS status, j.attempts AS attempts`,
        { id, error, maxAttempts: neo4j.int(maxAttempts), backoff: neo4j.int(backoff) },
      );
      return result.records.length ? toObject(result.records[0]) : { status: "missing" };
    } finally {
      await session.close();
    }
  }

  async requeueFailedJob(id: string) {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (j:Job {id: $id})
         WHERE j.status = 'failed'
         SET j.status = 'queued', j.attempts = 0, j.availableAt = datetime(),
             j.error = null, j.completedAt = null, j.leaseUntil = null
         RETURN j.id AS id, j.status AS status`,
        { id },
      );
      return result.records.length ? toObject(result.records[0]) : null;
    } finally {
      await session.close();
    }
  }

  async createMemory(input: CuratedMemory) {
    const embedding = this.embeddings.enabled ? await this.embeddings.embedDocuments([input.content]).then(([vector]) => vector) : null;
    const memory = {
      id: input.id ?? crypto.randomUUID(),
      content: input.content,
      category: input.category ?? "fact",
      status: input.status ?? "active",
      confidence: input.confidence ?? 1,
      metadata: JSON.stringify(input.metadata ?? {}),
      subject: input.subject ?? null,
      predicate: input.predicate ?? null,
      object: input.object ?? null,
      embedding,
      validFrom: input.validFrom ?? new Date().toISOString(),
      validUntil: input.validUntil ?? null,
      userId: input.userId ?? null,
      projectId: input.projectId ?? null,
      sessionId: input.sessionId ?? null,
    };
    const session = this.driver.session({ database: this.database });
    let conflicts: Array<{ id: string; relation: "reinforces" | "contradicts" }> = [];
    try {
      await session.executeWrite(async (tx) => {
        await tx.run(
          `MERGE (m:Memory {id: $memory.id})
           ON CREATE SET m += $memory, m.reinforcementCount = 0
           ON MATCH SET m += $memory,
                      m.reinforcementCount = coalesce(m.reinforcementCount, 0) + 1`,
          { memory },
        );
        await tx.run(
          `UNWIND $sourceEventIds AS eventId
           MATCH (m:Memory {id: $memoryId}), (e:Event {id: eventId})
           MERGE (m)-[:SUPPORTED_BY]->(e)`,
          { memoryId: memory.id, sourceEventIds: input.sourceEventIds ?? [] },
        );
        await tx.run(
          `MATCH (m:Memory {id: $memoryId})
           UNWIND $entityIds AS entityId
           MATCH (e:Entity {id: entityId})
           MERGE (m)-[r:ABOUT]->(e)
           SET r.weight = 1.0`,
          { memoryId: memory.id, entityIds: input.entityIds ?? [] },
        );
        if (input.supersedesMemoryId) {
          await tx.run(
            `MATCH (new:Memory {id: $newId}), (old:Memory {id: $oldId})
             SET old.status = 'superseded', old.validUntil = coalesce(old.validUntil, $validFrom)
             MERGE (new)-[:SUPERSEDES]->(old)`,
            { newId: memory.id, oldId: input.supersedesMemoryId, validFrom: memory.validFrom },
          );
        }
        const conflictResult = await tx.run(
          `MATCH (m:Memory {id: $memoryId}), (other:Memory)
           WHERE other.id <> m.id
             AND m.status = 'active' AND other.status = 'active'
             AND m.subject IS NOT NULL AND m.predicate IS NOT NULL
             AND other.subject = m.subject AND other.predicate = m.predicate
             AND ((m.userId IS NULL AND other.userId IS NULL) OR other.userId = m.userId)
             AND ((m.projectId IS NULL AND other.projectId IS NULL) OR other.projectId = m.projectId)
           WITH m, other, CASE WHEN coalesce(m.object, '') = coalesce(other.object, '') THEN 'reinforces' ELSE 'contradicts' END AS relation
           FOREACH (_ IN CASE WHEN relation = 'contradicts' THEN [1] ELSE [] END |
             MERGE (m)-[r:CONTRADICTS]->(other)
             SET r.weight = 0.8, m.contradictionStatus = 'contested', other.contradictionStatus = 'contested')
           FOREACH (_ IN CASE WHEN relation = 'reinforces' THEN [1] ELSE [] END |
             MERGE (m)-[r:REINFORCES]->(other)
             SET r.weight = 0.6)
           RETURN collect({id: other.id, relation: relation}) AS conflicts`,
          { memoryId: memory.id },
        );
        if (conflictResult.records.length) conflicts = conflictResult.records[0].get("conflicts") as Array<{ id: string; relation: "reinforces" | "contradicts" }>;
        await tx.run(
          `MERGE (j:Job {id: $jobId})
           ON CREATE SET j.kind = 'reconsolidate_memory', j.targetId = $memoryId,
                         j.status = 'queued', j.attempts = 0,
                         j.availableAt = datetime(), j.createdAt = datetime()`,
          { jobId: `reconsolidate-memory:${memory.id}`, memoryId: memory.id },
        );
      });
      return { ...memory, conflicts };
    } finally {
      await session.close();
    }
  }

  async listMemories(scope: MemoryScope = {}, limit = 50, status?: string) {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (m:Memory)
         WHERE ($userId IS NULL OR m.userId = $userId)
           AND ($projectId IS NULL OR m.projectId = $projectId)
           AND ($sessionId IS NULL OR m.sessionId = $sessionId)
           AND ($status IS NULL OR m.status = $status)
         OPTIONAL MATCH (m)-[:SUPPORTED_BY]->(e:Event)
         WITH m, collect(e.id)[0..10] AS sourceEventIds
         OPTIONAL MATCH (m)-[:SUPERSEDES]->(old:Memory)
         WITH m, sourceEventIds, collect(old.id)[0] AS supersedesMemoryId
         OPTIONAL MATCH (newer:Memory)-[:SUPERSEDES]->(m)
         WITH m, sourceEventIds, supersedesMemoryId, collect(newer.id) AS supersededByIds
         RETURN m.id AS id, m.content AS content, m.category AS category, m.status AS status,
                m.confidence AS confidence, m.metadata AS metadata, m.subject AS subject,
                m.predicate AS predicate, m.object AS object, m.validFrom AS validFrom,
                m.validUntil AS validUntil, m.userId AS userId, m.projectId AS projectId,
                m.sessionId AS sessionId, sourceEventIds, supersedesMemoryId, supersededByIds
         ORDER BY m.validFrom DESC
         LIMIT $limit`,
        { userId: scope.userId ?? null, projectId: scope.projectId ?? null, sessionId: scope.sessionId ?? null, status: status ?? null, limit: neo4j.int(Math.min(Math.max(limit, 1), 200)) },
      );
      return result.records.map((record) => serializeProperties(toObject(record)));
    } finally {
      await session.close();
    }
  }

  async memoryImpact(id: string) {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (m:Memory {id: $id})
         OPTIONAL MATCH (m)-[:SUPPORTED_BY]->(source:Event)
         OPTIONAL MATCH (replacement:Memory)-[:SUPERSEDES]->(m)
         RETURN m.id AS id, m.status AS status,
                collect(DISTINCT source.id) AS sourceEventIds,
                collect(DISTINCT replacement.id) AS supersededByIds`,
        { id },
      );
      return result.records.length ? serializeProperties(toObject(result.records[0])) : null;
    } finally {
      await session.close();
    }
  }

  async retractMemory(id: string, reason?: string) {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (m:Memory {id: $id})
         SET m.status = 'retracted', m.retractedAt = datetime(), m.retractionReason = $reason
         RETURN m.id AS id, m.status AS status`,
        { id, reason: reason ?? null },
      );
      return result.records.length ? toObject(result.records[0]) : null;
    } finally {
      await session.close();
    }
  }

  async recordMemoryUtility(id: string, useful: boolean, feedback?: string) {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.executeWrite((tx) => tx.run(
        `MATCH (m:Memory {id: $id})
         SET m.utilityUses = coalesce(m.utilityUses, 0) + 1,
             m.utilitySuccesses = coalesce(m.utilitySuccesses, 0) + CASE WHEN $useful THEN 1 ELSE 0 END,
             m.lastUsedAt = datetime(),
             m.lastUtilityFeedback = CASE WHEN $feedback IS NULL THEN m.lastUtilityFeedback ELSE $feedback END
         RETURN m.id AS id, m.utilityUses AS utilityUses, m.utilitySuccesses AS utilitySuccesses,
                toFloat(m.utilitySuccesses) / CASE WHEN m.utilityUses = 0 THEN 1 ELSE m.utilityUses END AS utilityScore`,
        { id, useful, feedback: feedback ?? null },
      ));
      return result.records.length ? serializeProperties(toObject(result.records[0])) : null;
    } finally {
      await session.close();
    }
  }

  async archiveMemory(id: string, reason?: string) {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.run(
        `MATCH (m:Memory {id: $id})
         SET m.status = 'archived', m.archivedAt = datetime(), m.archiveReason = $reason
         RETURN m.id AS id, m.status AS status, m.archivedAt AS archivedAt, m.archiveReason AS archiveReason`,
        { id, reason: reason ?? null },
      );
      return result.records.length ? serializeProperties(toObject(result.records[0])) : null;
    } finally {
      await session.close();
    }
  }

  async reconsolidateMemory(memoryId: string) {
    const session = this.driver.session({ database: this.database });
    try {
      const result = await session.executeWrite((tx) => tx.run(
        `MATCH (m:Memory {id: $memoryId})
         WITH m, m.userId AS memoryUserId, m.projectId AS memoryProjectId
         MATCH (other:Memory)
         WHERE other.id <> m.id
           AND other.status = 'active'
           AND m.status = 'active'
           AND m.subject IS NOT NULL AND m.predicate IS NOT NULL
           AND other.subject = m.subject AND other.predicate = m.predicate
           AND ((memoryUserId IS NULL AND other.userId IS NULL) OR other.userId = memoryUserId)
           AND ((memoryProjectId IS NULL AND other.projectId IS NULL) OR other.projectId = memoryProjectId)
         WITH m, other,
              CASE WHEN coalesce(m.object, '') = coalesce(other.object, '') THEN 'reinforces' ELSE 'contradicts' END AS relation
         FOREACH (_ IN CASE WHEN relation = 'contradicts' THEN [1] ELSE [] END |
           MERGE (m)-[:CONTRADICTS]->(other)
           SET m.contradictionStatus = 'contested', other.contradictionStatus = 'contested')
         FOREACH (_ IN CASE WHEN relation = 'reinforces' THEN [1] ELSE [] END |
           MERGE (m)-[:REINFORCES]->(other))
         RETURN collect({id: other.id, relation: relation}) AS links`,
        { memoryId },
      ));
      return result.records.length ? result.records[0].get("links") : [];
    } finally {
      await session.close();
    }
  }

  async upsertEntity(input: Entity) {
    const entity = {
      id: input.id ?? `${input.userId ?? "global"}:${input.projectId ?? "global"}:${input.type}:${input.name.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`,
      name: input.name,
      type: input.type,
      aliases: input.aliases ?? [],
      description: input.description ?? null,
      userId: input.userId ?? null,
      projectId: input.projectId ?? null,
      sessionId: input.sessionId ?? null,
    };
    const session = this.driver.session({ database: this.database });
    try {
      await session.run(
        `MERGE (e:Entity {id: $entity.id})
         ON CREATE SET e += $entity
         ON MATCH SET e.name = $entity.name, e.type = $entity.type,
                      e.aliases = $entity.aliases,
                      e.description = coalesce($entity.description, e.description)
         WITH e
         UNWIND $entity.aliases AS alias
         MATCH (other:Entity)
         WHERE other.id <> e.id
           AND (toLower(other.name) = toLower(alias)
             OR any(existing IN coalesce(other.aliases, []) WHERE toLower(existing) = toLower(alias)))
         MERGE (e)-[r:SAME_AS]->(other)
         SET r.weight = 0.9
         RETURN e`,
        { entity },
      );
      return entity;
    } finally {
      await session.close();
    }
  }

  async upsertDocument(input: DocumentInput) {
    const document = {
      id: input.id ?? crypto.randomUUID(),
      title: input.title,
      source: input.source,
      content: input.content ?? null,
      metadata: JSON.stringify(input.metadata ?? {}),
      userId: input.userId ?? null,
      projectId: input.projectId ?? null,
      sessionId: input.sessionId ?? null,
    };
    const session = this.driver.session({ database: this.database });
    try {
      await session.run(
        `MERGE (d:Document {id: $document.id})
         ON CREATE SET d += $document
         ON MATCH SET d.title = $document.title, d.source = $document.source,
                      d.content = coalesce($document.content, d.content), d.metadata = $document.metadata
         RETURN d`,
        { document },
      );
      return document;
    } finally {
      await session.close();
    }
  }

  async upsertChunk(input: ChunkInput) {
    const embedding = input.embedding ?? (this.embeddings.enabled ? await this.embeddings.embedDocuments([input.content]).then(([vector]) => vector) : null);
    const chunk = {
      id: input.id ?? crypto.randomUUID(),
      content: input.content,
      ordinal: input.ordinal,
      embedding,
      userId: input.userId ?? null,
      projectId: input.projectId ?? null,
      sessionId: input.sessionId ?? null,
    };
    const session = this.driver.session({ database: this.database });
    try {
      await session.executeWrite(async (tx) => {
        await tx.run(
          `MERGE (c:Chunk {id: $chunk.id})
           ON CREATE SET c += $chunk
           ON MATCH SET c.content = $chunk.content, c.ordinal = $chunk.ordinal, c.embedding = $chunk.embedding
           WITH c
           MATCH (d:Document {id: $documentId})
           MERGE (d)-[:HAS_CHUNK]->(c)`,
          { chunk, documentId: input.documentId },
        );
        await tx.run(
          `MATCH (c:Chunk {id: $chunkId})
           UNWIND $entityIds AS entityId
           MATCH (e:Entity {id: entityId})
           MERGE (c)-[r:MENTIONS]->(e)
           SET r.weight = 0.7`,
          { chunkId: chunk.id, entityIds: input.entityIds ?? [] },
        );
      });
      return chunk;
    } finally {
      await session.close();
    }
  }

  async createClaim(input: ClaimInput) {
    const claim = {
      id: input.id ?? crypto.randomUUID(),
      statement: input.statement,
      predicate: input.predicate,
      confidence: input.confidence ?? 1,
      userId: input.userId ?? null,
      projectId: input.projectId ?? null,
      sessionId: input.sessionId ?? null,
    };
    const session = this.driver.session({ database: this.database });
    try {
      await session.executeWrite(async (tx) => {
        await tx.run(
          `MERGE (c:Claim {id: $claim.id})
           ON CREATE SET c += $claim
           ON MATCH SET c += $claim
           WITH c
           MATCH (s:Entity {id: $subjectEntityId})
           MERGE (c)-[subjectRelation:ABOUT]->(s)
           SET subjectRelation.weight = 0.8
           WITH c
           OPTIONAL MATCH (o:Entity {id: $objectEntityId})
           FOREACH (_ IN CASE WHEN o IS NULL THEN [] ELSE [1] END |
             MERGE (c)-[objectRelation:ABOUT]->(o)
             SET objectRelation.weight = 0.8)`,
          { claim, subjectEntityId: input.subjectEntityId, objectEntityId: input.objectEntityId ?? null },
        );
        await tx.run(
          `MATCH (c:Claim {id: $claimId})
           UNWIND $sourceChunkIds AS chunkId
           MATCH (chunk:Chunk {id: chunkId})
           MERGE (c)-[:SUPPORTED_BY]->(chunk)`,
          { claimId: claim.id, sourceChunkIds: input.sourceChunkIds ?? [] },
        );
        await tx.run(
          `MATCH (c:Claim {id: $claimId})
           UNWIND $sourceEventIds AS eventId
           MATCH (event:Event {id: eventId})
           MERGE (c)-[:SUPPORTED_BY]->(event)`,
          { claimId: claim.id, sourceEventIds: input.sourceEventIds ?? [] },
        );
      });
      return claim;
    } finally {
      await session.close();
    }
  }

  async upsertProcedure(input: ProcedureInput) {
    const procedure = {
      id: input.id ?? crypto.randomUUID(),
      title: input.title,
      goal: input.goal,
      steps: JSON.stringify(input.steps),
      preconditions: JSON.stringify(input.preconditions ?? []),
      knownFailures: JSON.stringify(input.knownFailures ?? []),
      confidence: input.confidence ?? 1,
      executable: input.executable ?? false,
      command: input.command ?? null,
      parameters: JSON.stringify(input.parameters ?? {}),
      safety: input.safety ?? "approval-required",
      userId: input.userId ?? null,
      projectId: input.projectId ?? null,
      sessionId: input.sessionId ?? null,
    };
    const session = this.driver.session({ database: this.database });
    try {
      await session.executeWrite(async (tx) => {
        await tx.run(
          `MERGE (p:Procedure {id: $procedure.id})
           ON CREATE SET p += $procedure
           ON MATCH SET p += $procedure`,
          { procedure },
        );
        await tx.run(
          `UNWIND $sourceEventIds AS eventId
           MATCH (p:Procedure {id: $procedureId}), (e:Event {id: eventId})
           MERGE (p)-[:SUPPORTED_BY]->(e)`,
          { procedureId: procedure.id, sourceEventIds: input.sourceEventIds ?? [] },
        );
      });
      return procedure;
    } finally {
      await session.close();
    }
  }

  async recall(query: string, scope: MemoryScope = {}, limit = 12, route: RetrievalRoute = "semantic") {
    limit = Math.min(Math.max(Math.trunc(limit) || 12, 1), 50);
    const plan = retrievalPlan(query, route);
    const retrievalMethods = new Set(scope.retrievalMethods ?? ["lexical", "vector", "graph"]);
    const session = this.driver.session({ database: this.database });
    try {
      const params = { query, userId: scope.userId ?? null, projectId: scope.projectId ?? null, sessionId: scope.sessionId ?? null, includeHistorical: scope.includeHistorical ?? false, includeTestData: scope.includeTestData ?? false, limit: neo4j.int(Math.max(limit * plan.candidateMultiplier, 20)) };
      const eventResult = await session.run(
          `CALL db.index.fulltext.queryNodes('event_fulltext', $query) YIELD node, score
           WHERE ($userId IS NULL OR node.userId = $userId)
             AND ($projectId IS NULL OR node.projectId = $projectId)
             AND ($sessionId IS NULL OR node.sessionId = $sessionId)
             AND ($includeTestData OR coalesce(node.source, '') <> 'test')
           RETURN 'event' AS type, node.id AS id, node.content AS content, node.kind AS kind,
                  node.occurredAt AS occurredAt, node.source AS source, node.projectId AS projectId,
                  null AS status, null AS confidence,
                  [{id: node.id, content: node.content}] AS evidence, score
           ORDER BY score DESC LIMIT $limit`, params,
        );
      const memoryResult = await session.run(
          `CALL db.index.fulltext.queryNodes('memory_fulltext', $query) YIELD node, score
           WHERE NOT node.status IN ['retracted', 'archived']
             AND ($includeTestData OR NOT EXISTS { MATCH (node)-[:SUPPORTED_BY]->(:Event {source: 'test'}) })
             AND ($includeHistorical OR node.status = 'active')
             AND ($includeHistorical OR (node.validFrom IS NULL OR datetime(node.validFrom) <= datetime()) AND (node.validUntil IS NULL OR datetime(node.validUntil) > datetime()))
             AND ($userId IS NULL OR node.userId = $userId)
             AND ($projectId IS NULL OR node.projectId = $projectId)
             AND ($sessionId IS NULL OR node.sessionId = $sessionId)
             AND ($includeTestData OR NOT EXISTS { MATCH (node)-[:SUPPORTED_BY]->(:Event {source: 'test'}) })
           RETURN 'memory' AS type, node.id AS id, node.content AS content, node.category AS kind,
                  node.validFrom AS occurredAt, 'curated' AS source, node.projectId AS projectId,
                  node.status AS status, node.confidence AS confidence,
                  coalesce(node.reinforcementCount, 0) AS reinforcementCount,
                  toFloat(coalesce(node.utilitySuccesses, 0)) / CASE WHEN coalesce(node.utilityUses, 0) = 0 THEN 1 ELSE node.utilityUses END AS utilityScore,
                  [(node)-[:SUPPORTED_BY]->(source:Event) | source.id][0..10] AS sourceEventIds,
                  [(node)-[:SUPPORTED_BY]->(source:Event) | {id: source.id, content: source.content}][0..10] AS evidence, score
           ORDER BY score DESC LIMIT $limit`, params,
        );
      const procedureResult = await session.run(
          `CALL db.index.fulltext.queryNodes('procedure_fulltext_v2', $query) YIELD node, score
           WHERE ($userId IS NULL OR node.userId = $userId)
             AND ($projectId IS NULL OR node.projectId = $projectId)
             AND ($sessionId IS NULL OR node.sessionId = $sessionId)
             AND ($includeTestData OR NOT EXISTS { MATCH (node)-[:SUPPORTED_BY]->(:Event {source: 'test'}) })
           RETURN 'procedure' AS type, node.id AS id,
                  node.title + ': ' + node.goal + ' ' + coalesce(node.steps, '') AS content, 'procedure' AS kind,
                  null AS occurredAt, 'procedural' AS source, node.projectId AS projectId,
                  'active' AS status, node.confidence AS confidence,
                  [(node)-[:SUPPORTED_BY]->(source:Event) | source.id][0..10] AS sourceEventIds,
                  [(node)-[:SUPPORTED_BY]->(source:Event) | {id: source.id, content: source.content}][0..10] AS evidence, score
           ORDER BY score DESC LIMIT $limit`, params,
        );
      const claimResult = await session.run(
        `CALL db.index.fulltext.queryNodes('claim_fulltext', $query) YIELD node, score
         WHERE ($userId IS NULL OR node.userId = $userId)
           AND ($projectId IS NULL OR node.projectId = $projectId)
           AND ($sessionId IS NULL OR node.sessionId = $sessionId)
           AND ($includeTestData OR (NOT EXISTS { MATCH (node)-[:SUPPORTED_BY]->(:Event {source: 'test'}) } AND NOT EXISTS { MATCH (node)-[:SUPPORTED_BY]->(:Chunk)<-[:HAS_CHUNK]-(:Document {source: 'test'}) }))
         RETURN 'claim' AS type, node.id AS id, node.statement AS content,
                node.predicate AS kind, null AS occurredAt, 'claim' AS source,
                node.projectId AS projectId, 'active' AS status,
                node.confidence AS confidence,
                [(node)-[:SUPPORTED_BY]->(source:Event) | source.id][0..10] AS sourceEventIds,
                [(node)-[:SUPPORTED_BY]->(source:Chunk) | source.id][0..10] AS sourceChunkIds,
                ([(node)-[:SUPPORTED_BY]->(eventSource:Event) | {id: eventSource.id, content: eventSource.content}] + [(node)-[:SUPPORTED_BY]->(chunkSource:Chunk) | {id: chunkSource.id, content: chunkSource.content}])[0..10] AS evidence, score
         ORDER BY score DESC LIMIT $limit`, params,
      );
      const chunkResult = await session.run(
        `CALL db.index.fulltext.queryNodes('chunk_fulltext', $query) YIELD node, score
         WHERE ($userId IS NULL OR node.userId = $userId)
           AND ($projectId IS NULL OR node.projectId = $projectId)
           AND ($includeTestData OR NOT EXISTS { MATCH (node)<-[:HAS_CHUNK]-(:Document {source: 'test'}) })
         RETURN 'chunk' AS type, node.id AS id, node.content AS content,
                'source_chunk' AS kind, null AS occurredAt, 'document' AS source,
                node.projectId AS projectId, 'active' AS status, null AS confidence,
                node.id AS sourceChunkId, [{id: node.id, content: node.content}] AS evidence, score
         ORDER BY score DESC LIMIT $limit`, params,
      );
      const lexical: Array<Record<string, unknown>> = [...eventResult.records, ...memoryResult.records, ...procedureResult.records, ...claimResult.records, ...chunkResult.records]
        .map((record) => ({ ...toObject(record), retrievalMethod: "lexical" }));
      const graph = retrievalMethods.has("graph") ? await this.graphRecall(query, scope, Math.min(plan.graphLimit, limit * 2)) : [];
      let vector: Array<Record<string, unknown>> = [];
      const lexicalTopScore = Number(lexical[0]?.score ?? 0);
      const earlyStop = plan.complexity === "simple" && lexical.length >= limit && lexicalTopScore >= 0.8;
      if (retrievalMethods.has("vector") && this.embeddings.enabled && !earlyStop) {
        const queryEmbedding = await this.embeddings.embedQuery(query);
        const vectorQueryMode = process.env.NEO4J_VECTOR_QUERY_MODE ?? "procedure";
        const memoryVectorPrefix = vectorQueryMode === "search"
          ? `MATCH (node:Memory)
             SEARCH node IN (VECTOR INDEX memory_embedding FOR $embedding LIMIT $candidateLimit)
             SCORE AS score
             WHERE`
          : `CALL db.index.vector.queryNodes('memory_embedding', $candidateLimit, $embedding) YIELD node, score
           WHERE`;
        const chunkVectorPrefix = vectorQueryMode === "search"
          ? `MATCH (node:Chunk)
             SEARCH node IN (VECTOR INDEX chunk_embedding FOR $embedding LIMIT $candidateLimit)
             SCORE AS score
             WHERE`
          : `CALL db.index.vector.queryNodes('chunk_embedding', $candidateLimit, $embedding) YIELD node, score
           WHERE`;
        const vectorResult = await session.run(
        `${memoryVectorPrefix} ($includeHistorical OR (node.validFrom IS NULL OR datetime(node.validFrom) <= datetime()) AND (node.validUntil IS NULL OR datetime(node.validUntil) > datetime()))
             AND NOT node.status IN ['retracted', 'archived']
           AND ($includeHistorical OR node.status = 'active')
           AND ($userId IS NULL OR node.userId = $userId)
           AND ($projectId IS NULL OR node.projectId = $projectId)
           AND ($sessionId IS NULL OR node.sessionId = $sessionId)
           AND ($includeTestData OR NOT EXISTS { MATCH (node)-[:SUPPORTED_BY]->(:Event {source: 'test'}) })
         RETURN 'memory' AS type, node.id AS id, node.content AS content,
                node.category AS kind, node.validFrom AS occurredAt,
                'vector' AS source, node.projectId AS projectId,
                node.status AS status, node.confidence AS confidence,
                coalesce(node.reinforcementCount, 0) AS reinforcementCount,
                toFloat(coalesce(node.utilitySuccesses, 0)) / CASE WHEN coalesce(node.utilityUses, 0) = 0 THEN 1 ELSE node.utilityUses END AS utilityScore,
                [(node)-[:SUPPORTED_BY]->(source:Event) | source.id][0..10] AS sourceEventIds,
                [(node)-[:SUPPORTED_BY]->(source:Event) | {id: source.id, content: source.content}][0..10] AS evidence, score
         ORDER BY score DESC LIMIT $limit`,
         { embedding: queryEmbedding, candidateLimit: neo4j.int(Math.max(limit * plan.candidateMultiplier, 20)), limit: neo4j.int(limit), userId: scope.userId ?? null, projectId: scope.projectId ?? null, sessionId: scope.sessionId ?? null, includeHistorical: scope.includeHistorical ?? false, includeTestData: scope.includeTestData ?? false },
        );
        const chunkVectorResult = await session.run(
          `${chunkVectorPrefix} ($userId IS NULL OR node.userId = $userId)
           AND ($projectId IS NULL OR node.projectId = $projectId)
           AND ($includeTestData OR NOT EXISTS { MATCH (node)<-[:HAS_CHUNK]-(:Document {source: 'test'}) })
           RETURN 'chunk' AS type, node.id AS id, node.content AS content,
                  'source_chunk' AS kind, null AS occurredAt, 'document' AS source,
                  node.projectId AS projectId, 'active' AS status, null AS confidence,
                  node.id AS sourceChunkId, [{id: node.id, content: node.content}] AS evidence, score
           ORDER BY score DESC LIMIT $limit`,
          { embedding: queryEmbedding, candidateLimit: neo4j.int(Math.max(limit * plan.candidateMultiplier, 20)), limit: neo4j.int(limit), userId: scope.userId ?? null, projectId: scope.projectId ?? null, sessionId: scope.sessionId ?? null, includeTestData: scope.includeTestData ?? false },
        );
        vector = [...vectorResult.records, ...chunkVectorResult.records].map((record) => ({ ...toObject(record), retrievalMethod: "vector" }));
      }
      const weights = route === "graph"
        ? { lexical: 0.25, vector: 0.2, graph: 0.55 }
        : route === "temporal"
          ? { lexical: 0.45, vector: 0.2, graph: 0.35 }
          : route === "procedural"
            ? { lexical: 0.5, vector: 0.2, graph: 0.3 }
            : { lexical: 0.4, vector: 0.4, graph: 0.2 };
      const candidates: Array<Record<string, unknown>> = (reciprocalRankFuse({
        lexical: retrievalMethods.has("lexical") ? lexical.filter((candidate): candidate is Record<string, unknown> & { type: string; id: string } => typeof candidate.type === "string" && typeof candidate.id === "string") : [],
        vector: retrievalMethods.has("vector") ? vector.filter((candidate): candidate is Record<string, unknown> & { type: string; id: string } => typeof candidate.type === "string" && typeof candidate.id === "string") : [],
        graph: retrievalMethods.has("graph") ? graph.filter((candidate): candidate is Record<string, unknown> & { type: string; id: string } => typeof candidate.type === "string" && typeof candidate.id === "string") : [],
      }, weights) as Array<Record<string, unknown>>)
        .sort((left, right) => routeScore(right, route) - routeScore(left, route))
        .slice(0, Math.max(limit * 4, limit));
      const benchmarkProjectId = process.env.BENCHMARK_PROJECT_ID ?? "benchmark-suite";
      const isolatedCandidates = !scope.includeTestData && !scope.projectId
        ? candidates.filter((candidate) => candidate.projectId !== benchmarkProjectId)
        : candidates;
      if (!this.reranker.enabled) return isolatedCandidates.slice(0, limit);
      const reranked = await this.reranker.rerank(query, isolatedCandidates.flatMap((candidate) => {
        if (typeof candidate.id !== "string" || typeof candidate.content !== "string") return [];
        return [{ id: `${candidate.type}:${candidate.id}`, content: candidate.content, score: Number(candidate.score ?? 0) }];
      }));
      return reranked.slice(0, limit).map((candidate) => {
        const separator = candidate.id.indexOf(":");
        const key = separator >= 0 ? candidate.id : `memory:${candidate.id}`;
        return { ...(isolatedCandidates.find((item) => `${item.type}:${item.id}` === key) ?? {}), score: candidate.score, retrievalMethod: "reranked" };
      });
    } finally {
      await session.close();
    }
  }

  private async graphRecall(query: string, scope: MemoryScope, limit: number) {
    const terms = query.toLowerCase().split(/[^a-z0-9_-]+/).filter((term) => term.length >= 3).slice(0, 8);
    if (!terms.length) return [] as Array<Record<string, unknown>>;
    const session = this.driver.session({ database: this.database });
    try {
      let personalized: Array<Record<string, unknown>> = [];
      try {
        const graphName = process.env.NEO4J_GDS_GRAPH ?? "personal-memory-context";
        const exists = await session.run("CALL gds.graph.exists($graphName) YIELD exists RETURN exists", { graphName });
        if (exists.records.length && Boolean(exists.records[0].get("exists"))) {
          const seeds = await session.run(
            `UNWIND $terms AS term
             MATCH (e:Entity)
             WHERE (toLower(e.name) CONTAINS term OR any(alias IN coalesce(e.aliases, []) WHERE toLower(alias) CONTAINS term))
               AND ($userId IS NULL OR e.userId IS NULL OR e.userId = $userId)
               AND ($projectId IS NULL OR e.projectId IS NULL OR e.projectId = $projectId)
             RETURN collect(DISTINCT id(e)) AS sourceNodes`,
            { terms, userId: scope.userId ?? null, projectId: scope.projectId ?? null },
          );
          const sourceNodes = seeds.records.length ? seeds.records[0].get("sourceNodes") : [];
          if (Array.isArray(sourceNodes) && sourceNodes.length) {
            const pageRank = await session.run(
              `CALL gds.pageRank.stream($graphName, {sourceNodes: $sourceNodes, dampingFactor: 0.85, maxIterations: 20})
               YIELD nodeId, score
               WITH gds.util.asNode(nodeId) AS node, score
               WHERE node:Memory
                 AND NOT node.status IN ['retracted', 'archived']
                 AND ($includeHistorical OR node.status = 'active')
                 AND ($includeHistorical OR (node.validFrom IS NULL OR datetime(node.validFrom) <= datetime()) AND (node.validUntil IS NULL OR datetime(node.validUntil) > datetime()))
                 AND ($userId IS NULL OR node.userId = $userId)
                 AND ($projectId IS NULL OR node.projectId = $projectId)
                 AND ($sessionId IS NULL OR node.sessionId = $sessionId)
               RETURN 'memory' AS type, node.id AS id, node.content AS content, node.category AS kind,
                      node.validFrom AS occurredAt, 'ppr' AS source, node.projectId AS projectId,
                      node.status AS status, node.confidence AS confidence,
                      coalesce(node.reinforcementCount, 0) AS reinforcementCount,
                      toFloat(coalesce(node.utilitySuccesses, 0)) / CASE WHEN coalesce(node.utilityUses, 0) = 0 THEN 1 ELSE node.utilityUses END AS utilityScore,
                      [(node)-[:SUPPORTED_BY]->(source:Event) | source.id][0..10] AS sourceEventIds,
                      [] AS sourceChunkIds,
                      [(node)-[:SUPPORTED_BY]->(source:Event) | {id: source.id, content: source.content}][0..10] AS evidence,
                      score
               ORDER BY score DESC LIMIT $limit`,
              { graphName, sourceNodes, userId: scope.userId ?? null, projectId: scope.projectId ?? null, sessionId: scope.sessionId ?? null, includeHistorical: scope.includeHistorical ?? false, limit: neo4j.int(limit) },
            );
            personalized = pageRank.records.map((record): Record<string, unknown> => ({ ...toObject(record), retrievalMethod: "graph" }));
          }
        }
      } catch {
        // GDS is optional. The weighted bounded traversal below is the fallback.
      }
      const result = await session.run(
        `UNWIND $terms AS term
         MATCH (e:Entity)
         WHERE (toLower(e.name) CONTAINS term OR any(alias IN coalesce(e.aliases, []) WHERE toLower(alias) CONTAINS term))
           AND ($includeTestData OR coalesce(e.source, '') <> 'test')
           AND ($userId IS NULL OR e.userId IS NULL OR e.userId = $userId)
           AND ($projectId IS NULL OR e.projectId IS NULL OR e.projectId = $projectId)
           AND ($sessionId IS NULL OR e.sessionId IS NULL OR e.sessionId = $sessionId)
         MATCH path=(m:Memory)-[:ABOUT|SAME_AS|RELATED_TO*1..2]-(e)
         WHERE NOT m.status IN ['retracted', 'archived']
           AND ($includeTestData OR NOT EXISTS { MATCH (m)-[:SUPPORTED_BY]->(:Event {source: 'test'}) })
           AND ($includeHistorical OR m.status = 'active')
           AND ($includeHistorical OR (m.validFrom IS NULL OR datetime(m.validFrom) <= datetime()) AND (m.validUntil IS NULL OR datetime(m.validUntil) > datetime()))
           AND ($userId IS NULL OR m.userId = $userId)
           AND ($projectId IS NULL OR m.projectId = $projectId)
           AND ($sessionId IS NULL OR m.sessionId = $sessionId)
         WITH m, min(length(path)) AS distance,
              max(reduce(product = 1.0, rel IN relationships(path) | product * coalesce(rel.weight, 1.0))) AS propagationWeight
         RETURN DISTINCT 'memory' AS type, m.id AS id, m.content AS content,
                m.category AS kind, m.validFrom AS occurredAt, 'graph' AS source,
                m.projectId AS projectId, m.status AS status, m.confidence AS confidence,
                coalesce(m.reinforcementCount, 0) AS reinforcementCount,
                toFloat(coalesce(m.utilitySuccesses, 0)) / CASE WHEN coalesce(m.utilityUses, 0) = 0 THEN 1 ELSE m.utilityUses END AS utilityScore,
                [(m)-[:SUPPORTED_BY]->(source:Event) | source.id][0..10] AS sourceEventIds,
                [] AS sourceChunkIds,
                [(m)-[:SUPPORTED_BY]->(source:Event) | {id: source.id, content: source.content}][0..10] AS evidence,
                (0.8 * propagationWeight) / distance AS score
         UNION ALL
         UNWIND $terms AS claimTerm
         MATCH (claimEntity:Entity)
         WHERE (toLower(claimEntity.name) CONTAINS claimTerm OR any(alias IN coalesce(claimEntity.aliases, []) WHERE toLower(alias) CONTAINS claimTerm))
           AND ($includeTestData OR coalesce(claimEntity.source, '') <> 'test')
           AND ($userId IS NULL OR claimEntity.userId IS NULL OR claimEntity.userId = $userId)
           AND ($projectId IS NULL OR claimEntity.projectId IS NULL OR claimEntity.projectId = $projectId)
           AND ($sessionId IS NULL OR claimEntity.sessionId IS NULL OR claimEntity.sessionId = $sessionId)
         MATCH (c:Claim)-[:ABOUT]->(claimEntity)
         WHERE ($userId IS NULL OR c.userId = $userId)
           AND ($projectId IS NULL OR c.projectId = $projectId)
           AND ($includeTestData OR (NOT EXISTS { MATCH (c)-[:SUPPORTED_BY]->(:Event {source: 'test'}) } AND NOT EXISTS { MATCH (c)-[:SUPPORTED_BY]->(:Chunk)<-[:HAS_CHUNK]-(:Document {source: 'test'}) }))
         RETURN DISTINCT 'claim' AS type, c.id AS id, c.statement AS content,
                c.predicate AS kind, null AS occurredAt, 'graph' AS source,
                c.projectId AS projectId, 'active' AS status, c.confidence AS confidence,
                0 AS reinforcementCount,
                0.0 AS utilityScore,
                [] AS sourceEventIds,
                [(c)-[:SUPPORTED_BY]->(sourceChunk:Chunk) | sourceChunk.id][0..10] AS sourceChunkIds,
                [(c)-[:SUPPORTED_BY]->(sourceChunk:Chunk) | {id: sourceChunk.id, content: sourceChunk.content}][0..10] AS evidence,
                0.6 AS score
         ORDER BY score DESC LIMIT $limit`,
         { terms, userId: scope.userId ?? null, projectId: scope.projectId ?? null, sessionId: scope.sessionId ?? null, includeHistorical: scope.includeHistorical ?? false, includeTestData: scope.includeTestData ?? false, limit: neo4j.int(limit) },
      );
      return [...personalized, ...result.records.map((record): Record<string, unknown> => ({ ...toObject(record), retrievalMethod: "graph" }))];
    } finally {
      await session.close();
    }
  }
}

function toObject(record: Neo4jRecord): Record<string, unknown> {
  return Object.fromEntries(record.keys.map((key) => [key, record.get(key)]));
}

function serializeProperties(properties: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(Object.entries(properties).map(([key, value]) => [key, serializeValue(value)]));
}

function serializeValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(serializeValue);
  if (value && typeof value === "object") {
    const candidate = value as { toNumber?: () => number; toString?: () => string };
    if (typeof candidate.toNumber === "function") return candidate.toNumber();
    if (value.constructor?.name === "DateTime" || value.constructor?.name === "Date") return String(value);
    return Object.fromEntries(Object.entries(value).map(([key, nested]) => [key, serializeValue(nested)]));
  }
  return value;
}

function routeScore(item: Record<string, unknown>, route: RetrievalRoute) {
  const score = Number(item.score ?? 0);
  const type = String(item.type ?? "");
  const method = String(item.retrievalMethod ?? "");
  const boost = route === "procedural" && type === "procedure"
    ? 0.35
    : route === "graph" && (type === "claim" || method === "graph")
      ? 0.25
      : route === "temporal" && (type === "event" || type === "memory")
        ? 0.1
        : route === "semantic" && (method === "vector" || type === "memory" || type === "chunk")
          ? 0.1
          : 0;
  const retention = memoryRetentionScore({
    occurredAt: item.occurredAt,
    confidence: item.confidence,
    category: item.kind,
    reinforcementCount: item.reinforcementCount,
  });
  const utility = Math.min(1, Math.max(0, Number(item.utilityScore ?? 0) || 0));
  return score + boost + retention * (route === "temporal" ? 0.08 : 0.025) + utility * 0.08;
}
