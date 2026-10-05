import { parseBenchmarkCases, type BenchmarkCase } from "./benchmark-cases";

type RecallResponse = { results: Array<{ content?: string }>; abstain?: boolean };
type Mode = "lexical" | "vector" | "graph" | "hybrid";

const apiUrl = (process.env.MEMORY_API_URL ?? "http://127.0.0.1:4781").replace(/\/$/, "");
const apiKey = process.env.MEMORY_API_KEY;
const projectId = process.env.BENCHMARK_PROJECT_ID ?? "benchmark-suite";
const caseFile = process.env.BENCHMARK_CASES_FILE;
const caseText = caseFile ? await Bun.file(caseFile).text() : await Bun.file(new URL("../benchmark/questions.json", import.meta.url)).text();
const cases = parseBenchmarkCases(caseText, process.env.BENCHMARK_CASES_FORMAT as "json" | "jsonl" | undefined) as BenchmarkCase[];
const datasetHash = Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(caseText)))).map((byte) => byte.toString(16).padStart(2, "0")).join("");
const requestedModes = (process.env.ABLATION_MODES ?? "lexical,graph,hybrid").split(",").map((mode) => mode.trim()).filter(Boolean) as Mode[];
const methods: Record<Mode, Array<"lexical" | "vector" | "graph">> = {
  lexical: ["lexical"],
  vector: ["vector"],
  graph: ["graph"],
  hybrid: ["lexical", "vector", "graph"],
};

const reports = [];
for (const mode of requestedModes) {
  if (!methods[mode]) throw new Error(`unknown ablation mode: ${mode}`);
  const measurements = [];
  for (const testCase of cases) {
    const started = performance.now();
    const response = await fetch(`${apiUrl}/v1/recall`, {
      method: "POST",
      headers: { "content-type": "application/json", ...(apiKey ? { authorization: `Bearer ${apiKey}` } : {}) },
      body: JSON.stringify({ query: testCase.query, projectId, limit: 10, retrievalMethods: methods[mode] }),
    });
    const body = await response.json() as RecallResponse;
    const text = body.results.map((result) => result.content ?? "").join(" ").toLowerCase();
    measurements.push({
      id: testCase.id,
      hit: testCase.expectedContains.every((term) => text.includes(term.toLowerCase())),
      abstentionCorrect: Boolean(body.abstain) === testCase.shouldAbstain,
      elapsedMs: Math.round((performance.now() - started) * 100) / 100,
      resultCount: body.results.length,
    });
  }
  const latencies = measurements.map((measurement) => measurement.elapsedMs).sort((a, b) => a - b);
  reports.push({
    mode,
    methods: methods[mode],
    cases: measurements.length,
    hitRate: measurements.filter((measurement) => measurement.hit).length / measurements.length,
    abstentionAccuracy: measurements.filter((measurement) => measurement.abstentionCorrect).length / measurements.length,
    meanLatencyMs: measurements.reduce((sum, measurement) => sum + measurement.elapsedMs, 0) / measurements.length,
    p95LatencyMs: latencies[Math.min(latencies.length - 1, Math.floor(latencies.length * 0.95))],
    measurements,
  });
}

const report = { apiUrl, projectId, dataset: { source: caseFile ?? "benchmark/questions.json", sha256: datasetHash, format: process.env.BENCHMARK_CASES_FORMAT ?? "json" }, evaluation: { model: process.env.BENCHMARK_MODEL_ID ?? "not specified", embedding: process.env.BENCHMARK_EMBEDDING_ID ?? "not specified", reranker: process.env.BENCHMARK_RERANKER_ID ?? "not specified" }, retrievalConfiguration: Object.fromEntries(requestedModes.map((mode) => [mode, methods[mode]])), reports };
if (process.env.ABLATION_OUTPUT_FILE) await Bun.write(process.env.ABLATION_OUTPUT_FILE, JSON.stringify(report, null, 2));
console.log(JSON.stringify(report, null, 2));
