import { createOpenAI } from "@ai-sdk/openai";
import { Agent } from "@mastra/core/agent";

const baseURL = process.env.OLLAMA_BASE_URL ?? "http://127.0.0.1:11434/v1";
const model = process.env.OLLAMA_MODEL ?? "qwen2.5:14b-instruct";
const ollama = createOpenAI({
  baseURL,
  apiKey: process.env.OLLAMA_API_KEY ?? "ollama-local",
});

export const factoryAgent = new Agent({
  id: "factory-agent",
  name: "Factory Agent",
  instructions: `You are the lead agent in MeinFactory, a local-first software factory.

Your job is to turn a user's software request into a clear, implementation-ready plan.
Always separate assumptions, proposed files, commands, tests, and risks.
Do not claim that code was changed, commands were run, or tests passed unless a tool result proves it.
Prefer small, reversible steps and ask for approval before destructive actions, commits, or network access.
The runtime is local-only. Never suggest sending source code or secrets to a hosted service.`,
  // Mastra 1.67 and the latest AI SDK provider currently expose equivalent
  // V4 model types from separate package paths; keep the boundary explicit.
  model: ollama(model) as any,
});
