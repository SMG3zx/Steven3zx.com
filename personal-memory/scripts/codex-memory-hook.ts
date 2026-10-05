import { createHash } from "node:crypto";
import { appendFile, mkdir, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";

type HookEvent = Record<string, unknown>;

const apiUrl = (process.env.MEMORY_API_URL ?? "http://127.0.0.1:4781").replace(/\/$/, "");
const userId = process.env.MEMORY_USER_ID ?? "default";
const maxContentChars = Number(process.env.MEMORY_HOOK_MAX_CHARS ?? 20000);
const spoolFile = process.env.MEMORY_HOOK_SPOOL_FILE ?? join(import.meta.dir, "..", "data", "codex-hook-spool.jsonl");

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value : undefined;
}

function contentText(value: unknown): string | undefined {
  if (typeof value === "string") return value;
  if (Array.isArray(value)) {
    const parts = value.map(contentText).filter(Boolean) as string[];
    return parts.length ? parts.join("\n") : undefined;
  }
  if (value && typeof value === "object") {
    const item = value as Record<string, unknown>;
    return stringValue(item.text) ?? stringValue(item.content) ?? stringValue(item.output);
  }
  return undefined;
}

function trimContent(value: string): string {
  return value.length > maxContentChars ? `${value.slice(0, maxContentChars)}\n[truncated]` : value;
}

function stableKey(...values: unknown[]): string {
  return createHash("sha256").update(JSON.stringify(values)).digest("hex").slice(0, 32);
}

async function send(body: Record<string, unknown>): Promise<boolean> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 8000);
  try {
    const response = await fetch(`${apiUrl}/v1/conversations`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
      signal: controller.signal,
    });
    return response.ok;
  } catch {
    return false;
  } finally {
    clearTimeout(timeout);
  }
}

async function flushSpool() {
  let raw: string;
  try { raw = await readFile(spoolFile, "utf8"); } catch { return; }
  const pending = raw.split(/\r?\n/).filter(Boolean);
  const remaining: string[] = [];
  for (const line of pending) {
    try {
      const body = JSON.parse(line) as Record<string, unknown>;
      if (!(await send(body))) remaining.push(line);
    } catch {
      remaining.push(line);
    }
  }
  if (remaining.length) {
    await Bun.write(spoolFile, `${remaining.join("\n")}\n`);
  } else {
    try { await Bun.write(spoolFile, ""); } catch { /* best effort */ }
  }
}

async function persist(event: HookEvent, message: Record<string, unknown>) {
  const sessionId = stringValue(event.session_id) ?? "unknown-session";
  const cwd = stringValue(event.cwd);
  const projectId = cwd?.replaceAll("\\", "/");
  const turnId = stringValue(event.turn_id) ?? stableKey(event.hook_event_name, message.content);
  const hookName = stringValue(event.hook_event_name) ?? "unknown";
  const body = {
    sessionId,
    userId,
    projectId,
    messages: [{
      ...message,
      idempotencyKey: `codex-hook:${sessionId}:${turnId}:${hookName}:${stableKey(message.kind, message.content)}`,
      metadata: { ...(message.metadata as Record<string, unknown> | undefined), hook: hookName },
    }],
  };
  await flushSpool();
  if (await send(body)) return;
  try {
    await mkdir(dirname(spoolFile), { recursive: true });
    await appendFile(spoolFile, `${JSON.stringify(body)}\n`, "utf8");
  } catch {
    // Memory capture must never block Codex, even if the local spool is unavailable.
  }
}

function assistantMessages(value: unknown, results: string[] = []): string[] {
  if (Array.isArray(value)) {
    for (const item of value) assistantMessages(item, results);
    return results;
  }
  if (!value || typeof value !== "object") return results;
  const item = value as Record<string, unknown>;
  if (item.role === "assistant") {
    const text = contentText(item.content) ?? contentText(item.text) ?? contentText(item.output);
    if (text?.trim()) results.push(text);
  }
  for (const child of Object.values(item)) assistantMessages(child, results);
  return results;
}

async function readLastAssistant(transcriptPath: string | undefined): Promise<string | undefined> {
  if (!transcriptPath) return undefined;
  try {
    const raw = await readFile(transcriptPath, "utf8");
    const values: unknown[] = [];
    for (const line of raw.split(/\r?\n/)) {
      if (!line.trim()) continue;
      try { values.push(JSON.parse(line)); } catch { /* tolerate non-JSON transcript lines */ }
    }
    const messages = assistantMessages(values);
    return messages.at(-1);
  } catch {
    return undefined;
  }
}

async function main() {
  let event: HookEvent;
  try { event = JSON.parse(await Bun.stdin.text()); } catch { return; }
  const hookName = stringValue(event.hook_event_name);

  if (hookName === "UserPromptSubmit") {
    const prompt = stringValue(event.prompt);
    if (prompt) await persist(event, { kind: "user_message", content: trimContent(prompt) });
  } else if (hookName === "Stop") {
    const response = await readLastAssistant(stringValue(event.transcript_path));
    if (response) await persist(event, { kind: "agent_message", content: trimContent(response) });
  } else if (hookName === "PostToolUse") {
    const toolName = stringValue(event.tool_name) ?? "unknown-tool";
    const input = event.tool_input;
    const output = event.tool_output ?? event.tool_result;
    const content = trimContent(JSON.stringify({ tool: toolName, input, output }));
    await persist(event, { kind: "tool_result", content, metadata: { toolName } });
  }
}

await main();
