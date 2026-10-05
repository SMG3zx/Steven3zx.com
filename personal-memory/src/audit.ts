import { randomUUID } from "node:crypto";

export type AuditRunKind = "chain" | "llm" | "tool" | "retrieval" | "validation" | "security";
export type AuditRunStatus = "running" | "success" | "error";

export type AuditRun = {
  id: string;
  traceId: string;
  parentRunId?: string;
  name: string;
  kind: AuditRunKind;
  status: AuditRunStatus;
  startedAt: string;
  endedAt?: string;
  durationMs?: number;
  input?: unknown;
  output?: unknown;
  metadata: Record<string, unknown>;
  tags: string[];
  error?: string;
};

export type AuditQuery = {
  traceId?: string;
  status?: AuditRunStatus;
  kind?: AuditRunKind;
  name?: string;
  limit?: number;
};

const SECRET_KEY = /(authorization|api[-_]?key|access[-_]?token|refresh[-_]?token|token|password|secret|credential)/i;
const SECRET_VALUE = /\b(?:sk|sess|tok|key|auth)[-_a-z0-9]{12,}\b/gi;
const DEFAULT_MAX_RUNS = 5_000;
const DEFAULT_RETENTION_HOURS = 24;

export function redact(value: unknown, depth = 0): unknown {
  if (depth > 8) return "[redacted-depth]";
  if (typeof value === "string") return value.replace(SECRET_VALUE, "[REDACTED]");
  if (Array.isArray(value)) return value.map((item) => redact(item, depth + 1));
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, SECRET_KEY.test(key) ? "[REDACTED]" : redact(item, depth + 1)]));
  }
  return value;
}

export class AuditRegistry {
  private readonly runs: AuditRun[] = [];
  private readonly maxRuns: number;
  private readonly retentionMs: number;
  private readonly filePath?: string;

  constructor(options: { maxRuns?: number; retentionHours?: number } = {}) {
    this.maxRuns = Math.min(100_000, Math.max(100, Math.floor(options.maxRuns ?? Number(process.env.AUDIT_MAX_RUNS ?? DEFAULT_MAX_RUNS))));
    const retentionHours = Math.max(0, Number(options.retentionHours ?? process.env.AUDIT_RETENTION_HOURS ?? DEFAULT_RETENTION_HOURS));
    this.retentionMs = retentionHours === 0 ? 0 : retentionHours * 60 * 60 * 1000;
    this.filePath = process.env.AUDIT_FILE || undefined;
  }

  async load() {
    if (!this.filePath) return;
    try {
      const parsed = JSON.parse(await Bun.file(this.filePath).text()) as unknown;
      if (Array.isArray(parsed)) this.runs.splice(0, this.runs.length, ...parsed.filter((run): run is AuditRun => Boolean(run && typeof run === "object" && "id" in run && "traceId" in run)));
      this.prune();
    } catch {
      // A missing or incomplete diagnostic file should not prevent the memory API from starting.
    }
  }

  private persist() {
    if (this.filePath) void Bun.write(this.filePath, JSON.stringify(this.runs));
  }

  private prune(now = Date.now()) {
    const cutoff = this.retentionMs ? now - this.retentionMs : 0;
    const retained = this.runs.filter((run) => !cutoff || Date.parse(run.startedAt) >= cutoff);
    this.runs.splice(0, this.runs.length, ...retained.slice(-this.maxRuns));
  }

  start(input: { name: string; kind: AuditRunKind; traceId?: string; parentRunId?: string; input?: unknown; metadata?: Record<string, unknown>; tags?: string[] }) {
    const run: AuditRun = {
      id: randomUUID(),
      traceId: input.traceId ?? randomUUID(),
      parentRunId: input.parentRunId,
      name: input.name,
      kind: input.kind,
      status: "running",
      startedAt: new Date().toISOString(),
      input: redact(input.input),
      metadata: redact(input.metadata ?? {}) as Record<string, unknown>,
      tags: input.tags ?? [],
    };
    this.runs.push(run);
    this.prune();
    this.persist();
    return run;
  }

  finish(run: AuditRun, output?: unknown, metadata?: Record<string, unknown>) {
    run.status = "success";
    run.endedAt = new Date().toISOString();
    run.durationMs = Math.max(0, Date.parse(run.endedAt) - Date.parse(run.startedAt));
    run.output = redact(output);
    if (metadata) run.metadata = { ...run.metadata, ...redact(metadata) as Record<string, unknown> };
    this.persist();
    return run;
  }

  fail(run: AuditRun, error: unknown, metadata?: Record<string, unknown>) {
    run.status = "error";
    run.endedAt = new Date().toISOString();
    run.durationMs = Math.max(0, Date.parse(run.endedAt) - Date.parse(run.startedAt));
    run.error = String(redact(error instanceof Error ? error.message : error));
    if (metadata) run.metadata = { ...run.metadata, ...redact(metadata) as Record<string, unknown> };
    this.persist();
    return run;
  }

  query(query: AuditQuery = {}) {
    this.prune();
    const limit = Math.min(200, Math.max(1, query.limit ?? 50));
    return this.runs.filter((run) =>
      (!query.traceId || run.traceId === query.traceId)
      && (!query.status || run.status === query.status)
      && (!query.kind || run.kind === query.kind)
      && (!query.name || run.name.toLowerCase().includes(query.name.toLowerCase()) || run.traceId.toLowerCase().includes(query.name.toLowerCase()))
    ).slice(-limit).reverse();
  }

  trace(traceId: string) {
    this.prune();
    return this.runs.filter((run) => run.traceId === traceId).sort((a, b) => Date.parse(a.startedAt) - Date.parse(b.startedAt));
  }

  clear() { this.runs.length = 0; this.persist(); }

  stats() {
    this.prune();
    return {
      count: this.runs.length,
      maxRuns: this.maxRuns,
      retentionHours: this.retentionMs ? this.retentionMs / (60 * 60 * 1000) : 0,
      oldestStartedAt: this.runs[0]?.startedAt ?? null,
      newestStartedAt: this.runs.at(-1)?.startedAt ?? null,
    };
  }
}
