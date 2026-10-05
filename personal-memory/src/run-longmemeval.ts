import { answerTermCoverage, parseLongMemEval, sessionRecall } from "./longmemeval";

const file = process.env.LONGMEMEVAL_FILE;
if (!file) throw new Error("LONGMEMEVAL_FILE must point to a local LongMemEval JSON file. Download an authorized release from https://github.com/xiaowu0162/LongMemEval-V2, then rerun with LONGMEMEVAL_FILE=path/to/file.json.");
const apiUrl = (process.env.MEMORY_API_URL ?? "http://127.0.0.1:4781").replace(/\/$/, "");
const apiKey = process.env.MEMORY_API_KEY;
const limit = Number(process.env.LONGMEMEVAL_LIMIT ?? Number.POSITIVE_INFINITY);
const datasetText = await Bun.file(file).text();
const datasetHash = Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(datasetText)))).map((byte) => byte.toString(16).padStart(2, "0")).join("");
const cases = parseLongMemEval(datasetText, limit);
const measurements = [];

async function post(path: string, body: unknown) {
  const response = await fetch(`${apiUrl}${path}`, { method: "POST", headers: { "content-type": "application/json", ...(apiKey ? { authorization: `Bearer ${apiKey}` } : {}) }, body: JSON.stringify(body) });
  if (!response.ok) throw new Error(`${path} returned ${response.status}: ${await response.text()}`);
  return response.json() as Promise<{ results?: Array<{ id?: string; content?: string }>; abstain?: boolean }>;
}

for (const instance of cases) {
  for (let offset = 0; offset < instance.events.length; offset += 100) await post("/v1/events/batch", { events: instance.events.slice(offset, offset + 100) });
  const started = performance.now();
  const body = await post("/v1/recall", { query: instance.question, userId: instance.userId, projectId: instance.projectId, limit: Number(process.env.LONGMEMEVAL_RECALL_LIMIT ?? 20), includeTestData: true });
  const elapsedMs = performance.now() - started;
  const resultIds = (body.results ?? []).map((result) => result.id ?? "");
  const recall = sessionRecall(instance, resultIds);
  const answer = (body.results ?? []).map((result) => result.content ?? "").join(" ");
  measurements.push({ questionId: instance.question_id, questionType: instance.question_type, datasetVersion: instance.datasetVersion, recallAny: recall.recallAny, recallAll: recall.recallAll, answerTermCoverage: answerTermCoverage(answer, instance.answer), abstention: Boolean(body.abstain), elapsedMs: Math.round(elapsedMs * 100) / 100, estimatedCostUsd: Number(process.env.MEMORY_RECALL_COST_USD ?? 0), goldSessions: recall.goldSessions, retrievedSessions: recall.retrievedSessions });
}

const report = { dataset: "LongMemEval", protocol: "retrieval-only adapter; upstream end-to-end judge not run", source: file, sha256: datasetHash, recallLimit: Number(process.env.LONGMEMEVAL_RECALL_LIMIT ?? 20), cases: measurements.length, datasetVersions: [...new Set(measurements.map((measurement) => measurement.datasetVersion))], recallAnyRate: measurements.filter((measurement) => measurement.recallAny).length / measurements.length, recallAllRate: measurements.filter((measurement) => measurement.recallAll).length / measurements.length, meanAnswerTermCoverage: measurements.reduce((sum, measurement) => sum + measurement.answerTermCoverage, 0) / measurements.length, meanLatencyMs: measurements.reduce((sum, measurement) => sum + measurement.elapsedMs, 0) / measurements.length, estimatedCostUsd: measurements.reduce((sum, measurement) => sum + measurement.estimatedCostUsd, 0), measurements };
if (process.env.LONGMEMEVAL_OUTPUT_FILE) await Bun.write(process.env.LONGMEMEVAL_OUTPUT_FILE, JSON.stringify(report, null, 2));
console.log(JSON.stringify(report, null, 2));
