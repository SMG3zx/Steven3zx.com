import { FaithfulnessJudge, lexicalFaithfulness } from "./faithfulness";
import { parseBenchmarkCases, type BenchmarkCase } from "./benchmark-cases";
type RecallResult = { content?: string; sourceEventIds?: unknown; sourceChunkIds?: unknown; evidence?: Array<{ content?: string }>; retrievalMethod?: unknown; retrievalMethods?: unknown };
type RecallResponse = { results: RecallResult[]; abstain?: boolean };

const apiUrl = (process.env.MEMORY_API_URL ?? "http://127.0.0.1:4781").replace(/\/$/, "");
const apiKey = process.env.MEMORY_API_KEY;
const projectId = process.env.BENCHMARK_PROJECT_ID ?? "benchmark-suite";
const caseFile = process.env.BENCHMARK_CASES_FILE;
const caseText = caseFile ? await Bun.file(caseFile).text() : await Bun.file(new URL("../benchmark/questions.json", import.meta.url)).text();
const cases = parseBenchmarkCases(caseText, process.env.BENCHMARK_CASES_FORMAT as "json" | "jsonl" | undefined) as BenchmarkCase[];
const datasetHash = Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(caseText)))).map((byte) => byte.toString(16).padStart(2, "0")).join("");
const measurements: Array<Record<string, unknown>> = [];
const faithfulnessJudge = new FaithfulnessJudge();
const baselineFile = process.env.BENCHMARK_BASELINE_FILE;
const outputFile = process.env.BENCHMARK_OUTPUT_FILE;

for (const testCase of cases) {
  const started = performance.now();
  const response = await fetch(`${apiUrl}/v1/recall`, {
    method: "POST",
    headers: { "content-type": "application/json", ...(apiKey ? { authorization: `Bearer ${apiKey}` } : {}) },
    body: JSON.stringify({ query: testCase.query, projectId, limit: 10 }),
  });
  const body = (await response.json()) as RecallResponse;
  const elapsedMs = performance.now() - started;
  const text = body.results.map((result) => result.content ?? "").join(" ").toLowerCase();
  const hit = testCase.expectedContains.every((term) => text.includes(term.toLowerCase()));
  const attributed = !testCase.requiresProvenance || body.results.some((result) => (Array.isArray(result.sourceEventIds) && result.sourceEventIds.length > 0) || (Array.isArray(result.sourceChunkIds) && result.sourceChunkIds.length > 0));
  const evidenceText = body.results.flatMap((result) => [
    result.content ?? "",
    ...(result.evidence ?? []).map((evidence) => evidence.content ?? ""),
  ]).join(" ").toLowerCase();
  const lexicalResult = !testCase.requiresProvenance || lexicalFaithfulness(text, [evidenceText], testCase.expectedContains);
  const externalJudge = testCase.requiresProvenance && faithfulnessJudge.enabled
    ? await faithfulnessJudge.judge({ query: testCase.query, answer: text, evidence: body.results.flatMap((result) => [result.content ?? "", ...(result.evidence ?? []).map((evidence) => evidence.content ?? "")]) })
    : null;
  const faithfulness = externalJudge?.faithful ?? lexicalResult;
  const abstentionCorrect = Boolean(body.abstain) === testCase.shouldAbstain;
  const methods = new Set(body.results.flatMap((result) => [
    ...(typeof result.retrievalMethod === "string" ? [result.retrievalMethod] : []),
    ...(Array.isArray(result.retrievalMethods) ? result.retrievalMethods.map(String) : []),
  ]));
  const reasoningSupport = !testCase.reasoningIntensive || (methods.has("graph") && body.results.some((result) => Array.isArray(result.sourceEventIds) || Array.isArray(result.sourceChunkIds)));
  const estimatedCostUsd = Number(process.env.MEMORY_RECALL_COST_USD ?? 0);
  measurements.push({ id: testCase.id, category: testCase.category, elapsedMs: Math.round(elapsedMs * 100) / 100, hit, attributed, faithfulness, reasoningSupport, retrievalMethods: [...methods], faithfulnessJudge: externalJudge?.judgedBy ?? "lexical-fallback", abstentionCorrect, estimatedCostUsd, resultCount: body.results.length, abstain: Boolean(body.abstain) });
}

const latencies = measurements.map((measurement) => Number(measurement.elapsedMs)).sort((a, b) => a - b);
const hits = measurements.filter((measurement) => measurement.hit).length;
const abstentionCorrect = measurements.filter((measurement) => measurement.abstentionCorrect).length;
const provenanceCases = measurements.filter((measurement) => cases.find((testCase) => testCase.id === measurement.id)?.requiresProvenance);
const attributed = provenanceCases.filter((measurement) => measurement.attributed).length;
const faithful = provenanceCases.filter((measurement) => measurement.faithfulness).length;
const reasoningCases = measurements.filter((measurement) => cases.find((testCase) => testCase.id === measurement.id)?.reasoningIntensive);
const reasoningSupport = reasoningCases.filter((measurement) => measurement.reasoningSupport).length;
const report = {
  apiUrl,
  dataset: { source: caseFile ?? "benchmark/questions.json", sha256: datasetHash, format: process.env.BENCHMARK_CASES_FORMAT ?? "json" },
  evaluation: { retrievalMethods: "service default", faithfulnessJudge: faithfulnessJudge.enabled ? "external" : "lexical-fallback", projectId, model: process.env.BENCHMARK_MODEL_ID ?? "not specified", embedding: process.env.BENCHMARK_EMBEDDING_ID ?? "not specified", reranker: process.env.BENCHMARK_RERANKER_ID ?? "not specified" },
  cases: measurements.length,
  hitRate: hits / measurements.length,
  abstentionAccuracy: abstentionCorrect / measurements.length,
  provenanceCases: provenanceCases.length,
  attributionRate: provenanceCases.length ? attributed / provenanceCases.length : 1,
  faithfulnessRate: provenanceCases.length ? faithful / provenanceCases.length : 1,
  reasoningSupportRate: reasoningCases.length ? reasoningSupport / reasoningCases.length : 1,
  estimatedCostUsd: measurements.reduce((total, measurement) => total + Number(measurement.estimatedCostUsd), 0),
  meanLatencyMs: latencies.reduce((total, value) => total + value, 0) / latencies.length,
  p95LatencyMs: latencies[Math.min(latencies.length - 1, Math.floor(latencies.length * 0.95))],
  measurements,
};
const metricKeys = ["hitRate", "abstentionAccuracy", "attributionRate", "faithfulnessRate", "reasoningSupportRate", "meanLatencyMs", "p95LatencyMs", "estimatedCostUsd"] as const;
const baseline = baselineFile && await Bun.file(baselineFile).exists()
  ? JSON.parse(await Bun.file(baselineFile).text()) as Record<string, unknown>
  : null;
const enrichedReport = baseline
  ? {
      ...report,
      baseline: Object.fromEntries(metricKeys.filter((key) => typeof baseline[key] === "number").map((key) => [key, baseline[key]])),
      delta: Object.fromEntries(metricKeys.filter((key) => typeof baseline[key] === "number").map((key) => [key, Number(report[key]) - Number(baseline[key])])),
    }
  : report;
if (outputFile) await Bun.write(outputFile, JSON.stringify(enrichedReport, null, 2));
console.log(JSON.stringify(enrichedReport, null, 2));

if (process.env.BENCHMARK_STRICT === "1") {
  const maxCost = Number(process.env.BENCHMARK_MAX_COST_USD ?? Number.POSITIVE_INFINITY);
  if (report.hitRate < 1 || report.abstentionAccuracy < 1 || report.attributionRate < 1 || report.faithfulnessRate < 1 || report.reasoningSupportRate < 1 || report.estimatedCostUsd > maxCost) {
    throw new Error("benchmark strict gate failed");
  }
  if (process.env.FAITHFULNESS_REQUIRE_JUDGE === "1" && !faithfulnessJudge.enabled) {
    throw new Error("benchmark strict gate requires FAITHFULNESS_JUDGE_BASE_URL");
  }
}
