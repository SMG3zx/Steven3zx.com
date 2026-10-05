import { spawn, type ChildProcess } from "node:child_process";
import { resolve } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

const root = resolve(import.meta.dirname, "..");
const serviceUrl = "http://127.0.0.1:4121";
let service: ChildProcess;

async function waitForHealth() {
  for (let attempt = 0; attempt < 30; attempt++) {
    try {
      const response = await fetch(`${serviceUrl}/health`);
      if (response.ok) return;
    } catch {
      // The child is still starting.
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error("Mastra service did not become healthy");
}

describe("local factory service", () => {
  beforeAll(async () => {
    service = spawn("bun", ["run", "src/server.ts"], {
      cwd: resolve(root, "mastra"),
      env: { ...process.env, PORT: "4121" },
      stdio: "ignore",
      windowsHide: true,
    });
    await waitForHealth();
  });

  afterAll(() => {
    service.kill();
  });

  it("reports a local-only model service", async () => {
    const response = await fetch(`${serviceUrl}/health`);
    expect(response.status).toBe(200);
    expect(await response.json()).toMatchObject({
      ok: true,
      service: "meinfactory-mastra",
      model: "qwen2.5:14b-instruct",
    });
  });

  it("validates task prompts before calling the model", async () => {
    const response = await fetch(`${serviceUrl}/api/agents/factory-agent/generate`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ prompt: "   " }),
    });
    expect(response.status).toBe(400);
    expect(await response.json()).toEqual({ error: "prompt is required" });
  });

  it("starts a factory flow and returns a trackable run", async () => {
    const response = await fetch(`${serviceUrl}/api/factory/runs`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ prompt: "Build a local notes app" }),
    });
    const run = (await response.json()) as { id: string; stage: string; status: string };
    expect(response.status).toBe(202);
    expect(run.id).toEqual(expect.any(String));
    expect(run.stage).toBe("planning");
    expect(run.status).toBe("Factory flow started");

    const statusResponse = await fetch(`${serviceUrl}/api/factory/runs/${run.id}`);
    expect(statusResponse.status).toBe(200);
    expect(await statusResponse.json()).toMatchObject({ id: run.id, prompt: "Build a local notes app" });
  });
});
