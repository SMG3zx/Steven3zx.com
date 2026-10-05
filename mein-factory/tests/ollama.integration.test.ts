import { spawn, type ChildProcess } from "node:child_process";
import { resolve } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

const serviceUrl = "http://127.0.0.1:4130";
let service: ChildProcess;

async function waitForHealth() {
  for (let attempt = 0; attempt < 30; attempt++) {
    try {
      if ((await fetch(`${serviceUrl}/health`)).ok) return;
    } catch {
      // The service is still starting.
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error("Mastra service did not become healthy");
}

describe("real Ollama factory flow", () => {
  beforeAll(async () => {
    const ollama = await fetch("http://127.0.0.1:11434/api/tags");
    expect(ollama.ok).toBe(true);
    const catalog = (await ollama.json()) as { models?: Array<{ name?: string }> };
    expect(catalog.models?.some((model) => model.name === "qwen2.5:14b-instruct")).toBe(true);

    service = spawn("bun", ["run", "src/server.ts"], {
      cwd: resolve(import.meta.dirname, "..", "mastra"),
      env: { ...process.env, PORT: "4130" },
      stdio: "ignore",
      windowsHide: true,
    });
    await waitForHealth();
  }, 20_000);

  afterAll(() => service?.kill());

  it("runs the prompt through Ollama and reaches the approval gate", async () => {
    const response = await fetch(`${serviceUrl}/api/factory/runs`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ prompt: "Reply with exactly: LOCAL_OLLAMA_OK" }),
    });
    expect(response.status).toBe(202);
    const created = (await response.json()) as { id: string };

    let run: { stage?: string; status?: string; plan?: string } = {};
    for (let attempt = 0; attempt < 90; attempt++) {
      await new Promise((resolve) => setTimeout(resolve, 1000));
      run = (await (await fetch(`${serviceUrl}/api/factory/runs/${created.id}`)).json()) as typeof run;
      if (run.stage === "approval" || run.stage === "complete" || run.stage === "failed") break;
    }

    expect(run.stage, run.status).toBe("complete");
    expect(run.plan).toContain("LOCAL_OLLAMA_OK");
  }, 120_000);
});
