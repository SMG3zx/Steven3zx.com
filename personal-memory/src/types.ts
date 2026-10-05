export type EventKind = "user_message" | "agent_message" | "tool_call" | "tool_result" | "decision" | "artifact" | "error";
export type JobKind = "consolidate_event" | "reflect_session" | "embed_document" | "extract_graph" | "reconsolidate_memory";
export type JobStatus = "queued" | "running" | "completed" | "failed";

export type MemoryCategory = "working" | "profile" | "fact" | "preference" | "decision" | "episode" | "belief" | "reflection" | "failure" | "semantic" | "procedure" | "summary" | "claim";
export type MemoryStatus = "active" | "superseded" | "archived" | "retracted";
export type RetrievalMethod = "lexical" | "vector" | "graph";

export type MemoryScope = {
  userId?: string;
  projectId?: string;
  sessionId?: string;
  includeHistorical?: boolean;
  includeTestData?: boolean;
  retrievalMethods?: RetrievalMethod[];
};

export type MemoryEvent = MemoryScope & {
  id?: string;
  kind: EventKind;
  content: string;
  source?: string;
  metadata?: Record<string, unknown>;
  occurredAt?: string;
  idempotencyKey?: string;
};

export type ConversationMessage = {
  id?: string;
  idempotencyKey?: string;
  kind: EventKind;
  content: string;
  source?: string;
  metadata?: Record<string, unknown>;
  occurredAt?: string;
};

export type ConversationInput = MemoryScope & {
  sessionId: string;
  messages: ConversationMessage[];
};

export type SessionSummaryInput = MemoryScope & {
  sessionId: string;
  content: string;
  sourceEventIds?: string[];
  confidence?: number;
  idempotencyKey?: string;
};

export type SessionReflectionInput = MemoryScope & {
  sessionId: string;
  content: string;
  lessons?: string[];
  failures?: string[];
  sourceEventIds?: string[];
  idempotencyKey?: string;
};

export type SessionReflection = {
  reflection: string;
  lessons: string[];
  failures: string[];
};

export type MemoryJob = {
  id: string;
  kind: JobKind;
  targetId: string;
  status?: JobStatus;
  availableAt?: string;
  attempts?: number;
  error?: string;
  leaseUntil?: string;
  approvalStatus?: "pending" | "approved" | "rejected";
  proposal?: string;
};

export type CuratedMemory = MemoryScope & {
  id?: string;
  content: string;
  category?: MemoryCategory;
  status?: MemoryStatus;
  confidence?: number;
  metadata?: Record<string, unknown>;
  sourceEventIds?: string[];
  entityIds?: string[];
  supersedesMemoryId?: string;
  subject?: string;
  predicate?: string;
  object?: string;
  validFrom?: string;
  validUntil?: string;
};

export type EntityType = "person" | "project" | "organization" | "technology" | "file" | "tool" | "concept" | "other";

export type Entity = MemoryScope & {
  id?: string;
  name: string;
  type: EntityType;
  aliases?: string[];
  description?: string;
};

export type DocumentInput = MemoryScope & {
  id?: string;
  title: string;
  source: string;
  content?: string;
  metadata?: Record<string, unknown>;
};

export type ChunkInput = MemoryScope & {
  id?: string;
  documentId: string;
  content: string;
  ordinal: number;
  embedding?: number[];
  entityIds?: string[];
};

export type ClaimInput = MemoryScope & {
  id?: string;
  statement: string;
  predicate: string;
  subjectEntityId: string;
  objectEntityId?: string;
  confidence?: number;
  sourceChunkIds?: string[];
  sourceEventIds?: string[];
};

export type ProcedureInput = MemoryScope & {
  id?: string;
  title: string;
  goal: string;
  steps: string[];
  preconditions?: string[];
  knownFailures?: string[];
  confidence?: number;
  sourceEventIds?: string[];
  executable?: boolean;
  command?: string;
  parameters?: Record<string, unknown>;
  safety?: "read-only" | "approval-required" | "trusted";
};

export type MemorySnapshot = {
  format: "personal-memory-snapshot";
  version: 1;
  exportedAt: string;
  nodes: Array<{ key: string; labels: string[]; properties: Record<string, unknown> }>;
  relationships: Array<{ sourceKey: string; type: string; targetKey: string; properties: Record<string, unknown> }>;
};
