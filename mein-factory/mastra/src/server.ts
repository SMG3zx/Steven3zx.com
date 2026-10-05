import { factoryAgent } from "./mastra/agents/factory-agent";

const port = Number(process.env.PORT ?? 4111);
const uiOrigin = process.env.UI_ORIGIN ?? "*";
type FlowStage = "planning" | "approval" | "building" | "review" | "complete" | "failed";
type FlowRun = { id: string; prompt: string; stage: FlowStage; status: string; plan?: string; error?: string; createdAt: string; autoApprove: boolean };
const runs = new Map<string, FlowRun>();

function json(data: unknown, status = 200) {
  return Response.json(data, {
    status,
    headers: { "Access-Control-Allow-Origin": uiOrigin },
  });
}

const server = Bun.serve({
  hostname: "127.0.0.1",
  port,
  async fetch(request) {
    const url = new URL(request.url);

    if (request.method === "OPTIONS") {
      return new Response(null, {
        status: 204,
        headers: {
          "Access-Control-Allow-Origin": uiOrigin,
          "Access-Control-Allow-Headers": "content-type",
          "Access-Control-Allow-Methods": "GET,POST,OPTIONS",
        },
      });
    }

    if (url.pathname === "/health" && request.method === "GET") {
      return json({ ok: true, service: "meinfactory-mastra", model: process.env.OLLAMA_MODEL ?? "qwen2.5:14b-instruct" });
    }

    if (url.pathname === "/api/agents/factory-agent/generate" && request.method === "POST") {
      const body = (await request.json()) as { prompt?: string; autoApprove?: boolean };
      if (!body.prompt?.trim()) return json({ error: "prompt is required" }, 400);
      const result = await factoryAgent.generate(body.prompt);
      return json({ text: result.text });
    }

    if (url.pathname === "/api/factory/runs" && request.method === "POST") {
      const body = (await request.json()) as { prompt?: string };
      if (!body.prompt?.trim()) return json({ error: "prompt is required" }, 400);
      const run: FlowRun = {
        id: crypto.randomUUID(),
        prompt: body.prompt.trim(),
        stage: "planning",
        status: "Factory flow started",
        createdAt: new Date().toISOString(),
        autoApprove: body.autoApprove !== false,
      };
      runs.set(run.id, run);
      void executeFactoryFlow(run);
      return json(run, 202);
    }

    const runMatch = url.pathname.match(/^\/api\/factory\/runs\/([^/]+)$/);
    if (runMatch && request.method === "GET") {
      const run = runs.get(runMatch[1]);
      return run ? json(run) : json({ error: "run not found" }, 404);
    }

    return json({ error: "not found" }, 404);
  },
});

async function executeFactoryFlow(run: FlowRun) {
  try {
    if (process.env.FACTORY_TEST_MODE === "1") {
      await new Promise((resolve) => setTimeout(resolve, 100));
      run.plan = "Operational test plan: the local factory flow reached the approval gate.";
      run.stage = run.autoApprove ? "complete" : "approval";
      run.status = run.autoApprove ? "Factory preview complete — approval required for writes" : "Plan ready — awaiting approval";
      return;
    }
    const result = await factoryAgent.generate(run.prompt);
    run.plan = result.text;
    if (run.autoApprove) {
      run.stage = "building";
      run.status = "Build preview running";
      await new Promise((resolve) => setTimeout(resolve, 200));
      run.stage = "review";
      run.status = "Reviewing proposed changes";
      await new Promise((resolve) => setTimeout(resolve, 200));
      run.stage = "complete";
      run.status = "Factory preview complete — approval required for writes";
      return;
    }
    run.stage = "approval";
    run.status = "Plan ready — awaiting approval";
  } catch (error) {
    run.stage = "failed";
    run.status = "Factory flow failed";
    run.error = error instanceof Error ? error.message : "Unknown local agent error";
  }
}

console.log(`MeinFactory Mastra service listening on http://${server.hostname}:${server.port}`);
