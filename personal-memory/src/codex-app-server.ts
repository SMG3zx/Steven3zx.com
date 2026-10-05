type RpcMessage = { id?: number; method?: string; result?: unknown; error?: { message?: string }; params?: Record<string, unknown> };

type ChatResult = { threadId: string; turnId: string; text: string; events: unknown[]; usage?: Record<string, number> };

/** Minimal JSON-RPC client for the supported local Codex App Server protocol. */
export class CodexAppServerClient {
  private process?: ReturnType<typeof Bun.spawn>;
  private nextId = 1;
  private initialized = false;
  private threadId?: string;
  private readonly pending = new Map<number, { resolve: (value: unknown) => void; reject: (error: Error) => void }>();
  private readonly turns = new Map<string, { text: string; events: unknown[]; resolve: (value: ChatResult) => void; reject: (error: Error) => void; threadId: string }>();
  private readonly bufferedTurns = new Map<string, { text: string; events: unknown[]; status?: "completed" | "failed" | "interrupted" }>();

  private ensureProcess() {
    if (this.process) return;
    const command = process.env.CODEX_APP_SERVER_COMMAND ?? "codex";
    this.process = Bun.spawn([command, "app-server", "--stdio"], { stdin: "pipe", stdout: "pipe", stderr: "pipe" });
    void this.readOutput();
  }

  private async readOutput() {
    const stdout = this.process?.stdout;
    if (!stdout || typeof stdout === "number") return;
    const reader = (stdout as ReadableStream<Uint8Array>).getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    while (true) {
      const next = await reader.read();
      if (next.done) break;
      buffer += decoder.decode(next.value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() ?? "";
      for (const line of lines) {
        if (!line.trim()) continue;
        try { this.handle(JSON.parse(line) as RpcMessage); } catch { /* ignore non-JSON diagnostics */ }
      }
    }
    const error = new Error("Codex App Server stopped");
    for (const pending of this.pending.values()) pending.reject(error);
    for (const turn of this.turns.values()) turn.reject(error);
    this.pending.clear();
    this.turns.clear();
    this.process = undefined;
    this.initialized = false;
  }

  private handle(message: RpcMessage) {
    if (message.id !== undefined && (message.result !== undefined || message.error)) {
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      if (message.error) pending.reject(new Error(message.error.message ?? "Codex App Server request failed"));
      else pending.resolve(message.result);
      return;
    }
    if (!message.method) return;
    const params = message.params ?? {};
    const turnId = String(params.turnId ?? (params.turn as Record<string, unknown> | undefined)?.id ?? "");
    if (!turnId) return;
    const turn = this.turns.get(turnId);
    const buffered = turn ? undefined : (this.bufferedTurns.get(turnId) ?? { text: "", events: [] });
    const state = turn ?? buffered!;
    state.events.push(message);
    if (message.method === "item/agentMessage/delta") {
      const delta = params.delta;
      if (typeof delta === "string") state.text += delta;
    }
    if (message.method === "turn/completed" || message.method === "turn/failed" || message.method === "turn/interrupted") {
      if (!state.text) state.text = extractText(params);
      if (turn) {
        if (message.method === "turn/completed") turn.resolve({ threadId: turn.threadId, turnId, text: turn.text, events: turn.events, usage: usageFromEvents(turn.events) });
        else turn.reject(new Error(`Codex turn ended with ${message.method.replace("turn/", "")}`));
        this.turns.delete(turnId);
      } else {
        buffered!.status = message.method.replace("turn/", "") as "completed" | "failed" | "interrupted";
        this.bufferedTurns.set(turnId, buffered!);
      }
    }
  }

  private async request(method: string, params: Record<string, unknown>) {
    this.ensureProcess();
    const id = this.nextId++;
    const result = new Promise<unknown>((resolve, reject) => this.pending.set(id, { resolve, reject }));
    const input = JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n";
    const stdin = this.process?.stdin;
    if (!stdin || typeof stdin === "number") throw new Error("Codex App Server stdin is unavailable");
    await (stdin as Bun.FileSink).write(input);
    await (stdin as Bun.FileSink).flush();
    return result;
  }

  private async notify(method: string, params: Record<string, unknown>) {
    this.ensureProcess();
    const stdin = this.process?.stdin;
    if (!stdin || typeof stdin === "number") throw new Error("Codex App Server stdin is unavailable");
    await (stdin as Bun.FileSink).write(JSON.stringify({ jsonrpc: "2.0", method, params }) + "\n");
    await (stdin as Bun.FileSink).flush();
  }

  async account() {
    await this.initialize();
    return this.request("account/read", { refreshToken: false });
  }

  async beginChatGPTLogin() {
    await this.initialize();
    return this.request("account/login/start", { type: "chatgptDeviceCode" });
  }

  async chat(message: string, options: { threadId?: string; model?: string; cwd?: string } = {}): Promise<ChatResult> {
    await this.initialize();
    const startThread = async () => String((await this.request("thread/start", { ...(options.model ? { model: options.model } : {}), ...(options.cwd ? { cwd: options.cwd } : {}) }) as { thread?: { id?: string } }).thread?.id ?? "");
    let threadId = options.threadId ?? this.threadId ?? await startThread();
    if (!threadId) throw new Error("Codex App Server did not return a thread ID");
    this.threadId = threadId;
    let response: { turn?: { id?: string } };
    try {
      response = await this.request("turn/start", { threadId, input: [{ type: "text", text: message }] }) as { turn?: { id?: string } };
    } catch (error) {
      if (!(error instanceof Error) || !/thread not found/i.test(error.message)) throw error;
      this.threadId = undefined;
      threadId = await startThread();
      if (!threadId) throw new Error("Codex App Server did not return a replacement thread ID");
      this.threadId = threadId;
      response = await this.request("turn/start", { threadId, input: [{ type: "text", text: message }] }) as { turn?: { id?: string } };
    }
    const turnId = String(response.turn?.id ?? "");
    if (!turnId) throw new Error("Codex App Server did not return a turn ID");
    return new Promise<ChatResult>((resolve, reject) => {
      const buffered = this.bufferedTurns.get(turnId);
      this.bufferedTurns.delete(turnId);
      if (buffered?.status === "completed") resolve({ threadId, turnId, text: buffered.text, events: buffered.events, usage: usageFromEvents(buffered.events) });
      else if (buffered?.status) reject(new Error(`Codex turn ended with ${buffered.status}`));
      else this.turns.set(turnId, { text: buffered?.text ?? "", events: buffered?.events ?? [], resolve, reject, threadId });
      if (buffered?.status) return;
      setTimeout(() => {
        const turn = this.turns.get(turnId);
        if (!turn) return;
        this.turns.delete(turnId);
        reject(new Error("Codex turn timed out"));
      }, Number(process.env.CODEX_CHAT_TIMEOUT_MS ?? 120_000));
    });
  }

  private async initialize() {
    this.ensureProcess();
    if (this.initialized) return;
    await this.request("initialize", { clientInfo: { name: "personal-memory", title: "Personal Memory", version: "0.1.0" }, capabilities: { experimentalApi: true } });
    await this.notify("initialized", {});
    this.initialized = true;
  }

  async close() {
    this.process?.kill();
    this.process = undefined;
    this.initialized = false;
  }
}

function extractText(value: unknown): string {
  if (typeof value === "string") return value;
  if (Array.isArray(value)) return value.map(extractText).join("");
  if (!value || typeof value !== "object") return "";
  const record = value as Record<string, unknown>;
  for (const key of ["text", "content", "message", "delta"]) {
    if (key in record) {
      const text = extractText(record[key]);
      if (text) return text;
    }
  }
  return "";
}

function usageFromEvents(events: unknown[]) {
  const usage: Record<string, number> = {};
  const visit = (value: unknown) => {
    if (!value || typeof value !== "object") return;
    if (Array.isArray(value)) { value.forEach(visit); return; }
    const record = value as Record<string, unknown>;
    const candidate = record.usage && typeof record.usage === "object" ? record.usage as Record<string, unknown> : record;
    for (const [key, raw] of Object.entries(candidate)) {
      if (typeof raw === "number" && /(token|cost|input|output|cached)/i.test(key)) usage[key] = raw;
    }
    Object.values(record).forEach(visit);
  };
  events.forEach(visit);
  return Object.keys(usage).length ? usage : undefined;
}
