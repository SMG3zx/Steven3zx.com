import { z } from "zod";
import type { MemoryEvent, SessionReflection } from "./types";

const extractionSchema = z.object({
  memories: z.array(z.object({
    content: z.string().min(1),
    category: z.enum(["working", "profile", "fact", "preference", "decision", "episode", "belief", "reflection", "failure", "semantic", "procedure", "summary", "claim"]).default("fact"),
    confidence: z.number().min(0).max(1).default(0.7),
    entityNames: z.array(z.string()).default([]),
    validFrom: z.string().optional(),
    validUntil: z.string().optional(),
  })).default([]),
  entities: z.array(z.object({
    name: z.string().min(1),
    type: z.enum(["person", "project", "organization", "technology", "file", "tool", "concept", "other"]).default("concept"),
    aliases: z.array(z.string()).default([]),
    description: z.string().optional(),
  })).default([]),
  claims: z.array(z.object({
    statement: z.string().min(1),
    predicate: z.string().min(1),
    subjectName: z.string().min(1),
    objectName: z.string().optional(),
    confidence: z.number().min(0).max(1).default(0.7),
  })).default([]),
  procedures: z.array(z.object({
    title: z.string().min(1),
    goal: z.string().min(1),
    steps: z.array(z.string().min(1)).min(1),
    preconditions: z.array(z.string()).default([]),
    knownFailures: z.array(z.string()).default([]),
    confidence: z.number().min(0).max(1).default(0.7),
  })).default([]),
});

const reflectionSchema = z.object({
  reflection: z.string().min(1),
  lessons: z.array(z.string().min(1)).default([]),
  failures: z.array(z.string().min(1)).default([]),
});

export type Extraction = z.infer<typeof extractionSchema>;

export interface Consolidator {
  extract(event: MemoryEvent): Promise<Extraction>;
  reflect?(events: MemoryEvent[]): Promise<SessionReflection>;
}

export class OpenAICompatibleConsolidator implements Consolidator {
  private readonly baseUrl = (process.env.MODEL_BASE_URL ?? "").replace(/\/$/, "");
  private readonly model = process.env.MODEL_NAME ?? "local-model";
  private readonly apiKey = process.env.MODEL_API_KEY;

  async extract(event: MemoryEvent) {
    if (!this.baseUrl) throw new Error("MODEL_BASE_URL is required for consolidation");
    const response = await fetch(`${this.baseUrl}/chat/completions`, {
      method: "POST",
      headers: { "content-type": "application/json", ...(this.apiKey ? { authorization: `Bearer ${this.apiKey}` } : {}) },
      body: JSON.stringify({
        model: this.model,
        temperature: 0,
        response_format: { type: "json_object" },
        messages: [
          { role: "system", content: "Extract only durable, source-grounded knowledge from the event. Return JSON with memories, entities, claims, and procedures. Do not invent facts. Use empty arrays when the event contains no durable information." },
          { role: "user", content: JSON.stringify({ kind: event.kind, content: event.content, source: event.source, occurredAt: event.occurredAt, metadata: event.metadata }) },
        ],
      }),
    });
    if (!response.ok) throw new Error(`model service returned ${response.status}`);
    const payload = (await response.json()) as { choices?: Array<{ message?: { content?: string } }> };
    const content = payload.choices?.[0]?.message?.content;
    if (!content) throw new Error("model service returned no extraction content");
    return extractionSchema.parse(JSON.parse(content.replace(/^```json\s*|\s*```$/g, "")));
  }

  async reflect(events: MemoryEvent[]) {
    if (!this.baseUrl) throw new Error("MODEL_BASE_URL is required for reflection");
    const response = await fetch(`${this.baseUrl}/chat/completions`, {
      method: "POST",
      headers: { "content-type": "application/json", ...(this.apiKey ? { authorization: `Bearer ${this.apiKey}` } : {}) },
      body: JSON.stringify({
        model: this.model,
        temperature: 0,
        response_format: { type: "json_object" },
        messages: [
          { role: "system", content: "Reflect only on the supplied session events. Return JSON with reflection, lessons, and failures. Do not invent facts. Failures must describe actionable lessons from explicit errors or unsuccessful tool results; use empty arrays when absent." },
          { role: "user", content: JSON.stringify(events.map((event) => ({ id: event.id, kind: event.kind, content: event.content, occurredAt: event.occurredAt, metadata: event.metadata }))) },
        ],
      }),
    });
    if (!response.ok) throw new Error(`model service returned ${response.status}`);
    const payload = (await response.json()) as { choices?: Array<{ message?: { content?: string } }> };
    const content = payload.choices?.[0]?.message?.content;
    if (!content) throw new Error("model service returned no reflection content");
    return reflectionSchema.parse(JSON.parse(content.replace(/^```json\s*|\s*```$/g, "")));
  }
}

/**
 * Explicitly opt-in offline consolidator for local UX and lifecycle tests.
 * It never infers durable memory from ordinary chat: callers must mark the
 * event metadata with { remember: true }.
 */
export class HeuristicConsolidator implements Consolidator {
  async extract(event: MemoryEvent): Promise<Extraction> {
    if (event.metadata?.remember !== true) return { memories: [], entities: [], claims: [], procedures: [] };
    return {
      memories: [{ content: event.content, category: "fact", confidence: 0.5, entityNames: [], validFrom: event.occurredAt }],
      entities: [],
      claims: [],
      procedures: [],
    };
  }

  async reflect(events: MemoryEvent[]): Promise<SessionReflection> {
    const remembered = events.filter((event) => event.metadata?.remember === true);
    return {
      reflection: remembered.length ? `Explicitly remembered ${remembered.length} event(s).` : "No events were explicitly marked for durable reflection.",
      lessons: [],
      failures: [],
    };
  }
}

export function createConsolidator(): Consolidator {
  return process.env.CONSOLIDATION_MODE === "heuristic" ? new HeuristicConsolidator() : new OpenAICompatibleConsolidator();
}

export const parseExtraction = (value: unknown) => extractionSchema.parse(value);
export const parseReflection = (value: unknown) => reflectionSchema.parse(value);
