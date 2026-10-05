import { createSimulation, evaluateSimulation, runSimulationAgainstApi, toOpenTelemetryTraces } from "./simulator";

const seed = Number(process.env.SIMULATION_SEED ?? 42);
const liveSimulation = process.env.SIMULATION_LIVE === "1";
const world = createSimulation(seed, liveSimulation ? `live-${crypto.randomUUID().slice(0, 12)}` : undefined);
const seeds = (process.env.SIMULATION_SEEDS ?? String(seed)).split(",").map(Number).filter(Number.isFinite);
const report = liveSimulation
  ? await runSimulationAgainstApi(world, process.env.MEMORY_API_URL ?? "http://127.0.0.1:4781", process.env.MEMORY_API_KEY)
  : seeds.length > 1
    ? { datasetVersion: world.datasetVersion, seeds, reports: seeds.map((matrixSeed) => evaluateSimulation(createSimulation(matrixSeed), createSimulation(matrixSeed).probes.map((probe) => ({ probeId: probe.id, answer: probe.expectedTerms.join(" "), memoryIds: probe.expectedMemoryIds, abstain: probe.shouldAbstain })))) }
    : evaluateSimulation(world, world.probes.map((probe) => ({ probeId: probe.id, answer: probe.expectedTerms.join(" "), memoryIds: probe.expectedMemoryIds, abstain: probe.shouldAbstain })));
const outputFile = process.env.SIMULATION_OUTPUT_FILE;
if (outputFile) await Bun.write(outputFile, JSON.stringify({ world, report }, null, 2));
if (process.env.SIMULATION_TRACE_FILE && "traces" in report) await Bun.write(process.env.SIMULATION_TRACE_FILE, JSON.stringify(toOpenTelemetryTraces(report.traces ?? []), null, 2));
function redactConsoleReport(value: unknown): unknown {
  if (!value || typeof value !== "object") return value;
  if (Array.isArray(value)) return value.map(redactConsoleReport);
  const record = { ...(value as Record<string, unknown>) };
  if (Array.isArray(record.results)) {
    record.results = record.results.map((item) => {
      if (!item || typeof item !== "object") return item;
      const result = { ...(item as Record<string, unknown>) };
      const expected = Array.isArray(result.expectedMemoryIds) ? result.expectedMemoryIds.length : undefined;
      const observed = Array.isArray(result.observedMemoryIds) ? result.observedMemoryIds.length : undefined;
      delete result.expectedMemoryIds;
      delete result.observedMemoryIds;
      if (expected !== undefined) result.expectedMemoryCount = expected;
      if (observed !== undefined) result.observedMemoryCount = observed;
      return result;
    });
  }
  return record;
}
const displayReport = process.env.SIMULATION_VERBOSE === "1" ? report : redactConsoleReport(report);
console.log(JSON.stringify({ world: { seed: world.seed, userId: world.userId, projectId: world.projectId, events: world.events.length, probes: world.probes.length }, report: displayReport }, null, 2));
