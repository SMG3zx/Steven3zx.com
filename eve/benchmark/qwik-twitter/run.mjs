import { spawn } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { performance } from 'node:perf_hooks';
import process from 'node:process';

const root = fileURLToPath(new URL('.', import.meta.url));
const port = 4178;
const base = `http://127.0.0.1:${port}`;
const durationMs = Number(process.env.BENCH_DURATION_MS || 5000);
const warmupMs = Number(process.env.BENCH_WARMUP_MS || 2000);
const rounds = Number(process.env.BENCH_ROUNDS || 3);
const levels = (process.env.BENCH_CONCURRENCIES || '1,32,128,512').split(',').map(Number);
const scenarios = process.env.BENCH_SCENARIOS?.split(',') || ['reads', 'search', 'writes', 'mixed'];
const runtimes = process.argv.slice(2).length ? process.argv.slice(2) : ['node', 'bun'];
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const executableFor = (runtime) => runtime === 'node' ? process.execPath : process.env.BUN_BIN || (process.platform === 'win32' ? path.join(process.env.USERPROFILE || '', '.bun', 'bin', 'bun.exe') : runtime);
async function getMetrics() { return (await fetch(`${base}/__metrics`)).json(); }
async function waitReady() { for (let attempt = 0; attempt < 150; attempt++) { try { if ((await fetch(`${base}/__ready`)).ok) return; } catch {} await sleep(20); } throw new Error('server did not become ready'); }
function requestFor(scenario, sequence) {
  const n = sequence % 20;
  if (scenario === 'reads' || (scenario === 'mixed' && n < 12)) return [`${base}/api/feed?offset=${n}`, {}];
  if (scenario === 'search' || (scenario === 'mixed' && n < 16)) return [`${base}/api/search?q=benchmark`, {}];
  if (scenario === 'writes' || (scenario === 'mixed' && n < 19)) return [`${base}/api/posts/${(n % 20) + 1}/like`, { method: 'POST' }];
  return [`${base}/api/posts`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ text: `interaction ${sequence}` }) }];
}
async function exercise(scenario, concurrency, end, latencies, errors) {
  let sequence = 0;
  const worker = async () => { while (performance.now() < end) { const current = sequence++; const [url, options] = requestFor(scenario, current); const started = performance.now(); try { const response = await fetch(url, options); if (!response.ok) errors.count++; await response.arrayBuffer(); } catch { errors.count++; } latencies.push(performance.now() - started); } };
  await Promise.all(Array.from({ length: concurrency }, worker));
}
async function runCase(runtime, scenario, concurrency, round) {
  const child = spawn(executableFor(runtime), ['server.mjs'], { cwd: root, stdio: ['ignore', 'ignore', 'inherit'], env: { ...process.env, PORT: String(port) } });
  const launched = performance.now(); await waitReady(); const startupMs = performance.now() - launched; await sleep(warmupMs);
  const latencies = []; const errors = { count: 0 }; const samples = []; const sampleTimer = setInterval(async () => { try { samples.push(await getMetrics()); } catch {} }, 100);
  await exercise(scenario, concurrency, performance.now() + durationMs, latencies, errors); clearInterval(sampleTimer); const finalMetrics = await getMetrics(); samples.push(finalMetrics); child.kill('SIGTERM'); await new Promise((resolve) => child.once('exit', resolve));
  latencies.sort((a, b) => a - b); const percentile = (p) => latencies[Math.min(latencies.length - 1, Math.floor(latencies.length * p))] || 0; const peak = samples.reduce((best, sample) => ({ rssBytes: Math.max(best.rssBytes, sample.rssBytes), heapUsedBytes: Math.max(best.heapUsedBytes, sample.heapUsedBytes), cpuUserMicros: Math.max(best.cpuUserMicros, sample.cpuUserMicros), cpuSystemMicros: Math.max(best.cpuSystemMicros, sample.cpuSystemMicros) }), { rssBytes: 0, heapUsedBytes: 0, cpuUserMicros: 0, cpuSystemMicros: 0 });
  return { runtime, scenario, concurrency, round, startupMs, durationMs, warmupMs, requests: latencies.length, interactionsPerSecond: latencies.length / (durationMs / 1000), errors: errors.count, latencyMs: { p50: percentile(.5), p95: percentile(.95), p99: percentile(.99), max: latencies.at(-1) || 0 }, peakRssMiB: peak.rssBytes / 1024 / 1024, peakHeapMiB: peak.heapUsedBytes / 1024 / 1024, cpuMs: (peak.cpuUserMicros + peak.cpuSystemMicros) / 1000, counters: finalMetrics.counters };
}
const results = [];
for (const runtime of runtimes) for (const scenario of scenarios) for (const concurrency of levels) for (let round = 1; round <= rounds; round++) { process.stdout.write(`Running ${runtime} / ${scenario} / c${concurrency} / round ${round}\n`); results.push(await runCase(runtime, scenario, concurrency, round)); }
await mkdir(path.join(root, 'results'), { recursive: true }); await writeFile(path.join(root, 'results', 'latest.json'), `${JSON.stringify({ generatedAt: new Date().toISOString(), config: { durationMs, warmupMs, rounds, levels, scenarios }, results }, null, 2)}\n`); console.table(results.map(({ runtime, scenario, concurrency, round, startupMs, interactionsPerSecond, errors, peakRssMiB, cpuMs, latencyMs }) => ({ runtime, scenario, concurrency, round, startupMs: startupMs.toFixed(1), ips: interactionsPerSecond.toFixed(1), p50: latencyMs.p50.toFixed(2), p95: latencyMs.p95.toFixed(2), p99: latencyMs.p99.toFixed(2), rssMiB: peakRssMiB.toFixed(1), cpuMs: cpuMs.toFixed(1), errors })));
