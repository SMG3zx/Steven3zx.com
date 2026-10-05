#!/usr/bin/env bun

import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";

const root = resolve(import.meta.dir);
const backendDir = join(root, "janus-rust");
const rustManifest = join(backendDir, "Cargo.toml");
const defaultComposeFile = join(root, "docker-compose.yml");
const binaryName = process.platform === "win32" ? "janus-api.exe" : "janus-api";
const binaryPath = join(backendDir, "target", "debug", binaryName);

loadEnv(join(root, ".env"));
loadEnv(join(root, ".env.local"));

const aliases = {
  local: "run",
  compile: "build",
  up: "stack-up",
  down: "stack-down",
  logs: "stack-logs",
  ps: "stack-ps",
};

const args = process.argv.slice(2);
const requestedTask = (args.shift() || "run").toLowerCase();
const commandName = aliases[requestedTask] || requestedTask;

function loadEnv(path) {
  if (!existsSync(path)) return;
  for (const rawLine of readFileSync(path, "utf8").split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;
    const separator = line.indexOf("=");
    if (separator < 1) continue;
    const key = line.slice(0, separator).trim();
    let value = line.slice(separator + 1).trim();
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }
    if (!process.env[key]) process.env[key] = value;
  }
}

function log(message) {
  console.log(`[janus] ${message}`);
}

function fail(message) {
  throw new Error(message);
}

function commandExists(command) {
  return Bun.which(command) !== null;
}

function requireCommand(command, hint = "") {
  if (!commandExists(command)) fail(`${command} is not installed or not available in PATH.${hint ? ` ${hint}` : ""}`);
}

function run(cmd, options = {}) {
  const { cwd = root, env = {}, quiet = false } = options;
  if (!quiet) log(cmd.join(" "));
  const result = Bun.spawnSync({
    cmd,
    cwd,
    env: { ...process.env, ...env },
    stdout: "inherit",
    stderr: "inherit",
  });
  if (result.exitCode !== 0) process.exit(result.exitCode || 1);
}

function cargo(args, options = {}) {
  requireCommand("cargo", "Install Rust and Cargo to build or test Janus.");
  run(["cargo", ...args], options);
}

function compose(args) {
  requireCommand("podman", "Install Podman to manage the local Janus stack.");
  const cwd = process.env.JANUS_PODMAN_COMPOSE_DIR || root;
  const file = process.env.JANUS_PODMAN_COMPOSE_FILE || defaultComposeFile;
  run(["podman", "compose", "-f", file, ...args], { cwd });
}

function build() {
  cargo(["build", "--manifest-path", rustManifest, "--bin", "janus-api"], { cwd: root });
  log(`built ${binaryName}`);
}

function ensureBinary() {
  if (!existsSync(binaryPath)) build();
}

function canBindPort(port) {
  try {
    const server = Bun.listen({
      hostname: "127.0.0.1",
      port,
      socket: { data() {} },
    });
    server.stop();
    return true;
  } catch {
    return false;
  }
}

function configuredPort() {
  const raw = process.env.API_ADDR || ":8080";
  const match = raw.match(/:(\d+)$/);
  return match ? Number(match[1]) : 8080;
}

function selectBackendPort() {
  const preferred = configuredPort();
  for (let port = preferred; port < preferred + 100; port += 1) {
    if (canBindPort(port)) return port;
  }
  fail(`No available API port found near ${preferred}. Set API_ADDR to a free port.`);
}

function backendEnv(port) {
  return {
    JANUS_HTTP_ADDRESS: `127.0.0.1:${port}`,
    JANUS_ENV: process.env.JANUS_ENV || "development",
    JANUS_AUTH_FILE: process.env.JANUS_AUTH_FILE || join(root, "janus-rust", "artifacts", "local-auth.bin"),
  };
}

function runBackend(extraArgs) {
  ensureBinary();
  const port = selectBackendPort();
  log(`using API port ${port}`);
  run([binaryPath, ...extraArgs], { env: backendEnv(port) });
}

async function waitForBackend(port) {
  const base = `http://127.0.0.1:${port}`;
  for (let attempt = 0; attempt < 80; attempt += 1) {
    try {
      const response = await fetch(`${base}/healthz`, { signal: AbortSignal.timeout(500) });
      if (response.status === 200) {
        const payload = await response.json().catch(() => null);
        if (payload?.ok === true || payload?.status === "ok") return base;
      }
    } catch {
      // Janus is still starting.
    }
    await Bun.sleep(250);
  }
  return null;
}

async function runDevelopment() {
  ensureBinary();
  const port = selectBackendPort();
  const backend = Bun.spawn([binaryPath], {
    cwd: root,
    env: { ...process.env, ...backendEnv(port) },
    stdout: "inherit",
    stderr: "inherit",
  });

  try {
    const apiBase = await waitForBackend(port);
    if (!apiBase) fail(`Janus did not become healthy on port ${port}.`);
    log(`Actix frontend and API: ${apiBase}`);
    await backend.exited;
  } finally {
    if (backend.exitCode === null) backend.kill();
  }
}

function backendServices() {
  return ["spacetime", "minio", "janus-api"];
}

function printHelp() {
  console.log(`Janus development CLI (Bun)\n
Usage: bun Janus.js <command> [...args]

  Core:
  dev                    Start the Actix frontend and API with automatic port selection
  run, backend-run       Build if needed, select a free API port, and run Janus
  build, compile         Build the Rust Janus API binary
  operator, cli           Run the dedicated Janus operator CLI

Containers:
  backend-up             Start backend services
  backend-down           Remove backend containers
  backend-logs [service] Follow backend logs
  backend-ps             Show backend service status
  stack-up               Build and start the local stack
  stack-up-build         Build images, then start the stack
  stack-rebuild          Rebuild images without cache, then start
  stack-down              Stop and remove the stack
  stack-logs [service]   Follow stack logs
  stack-ps               Show stack status

Frontend:
  web-dev                Alias for the Actix frontend and API development server
  web-build              Verify the embedded Actix frontend by building the Rust API

Quality:
  godoc                  Generate docs/godoc.md for every Go package/function
  tech-debt [path]       Analyze every Go file for technical debt with Jev
                         Add --json for the full machine-readable report
  backend-test           Run the Rust test suite
  backend-build          Compile the Rust workspace

Aliases: local, up, down, logs, ps`);
}

const techDebtExtensions = new Set([".go"]);

const techDebtIgnoredDirectories = new Set([
  ".git", ".janus", ".next", ".turbo", "__pycache__", "bin", "build", "coverage",
  "dist", "node_modules", "target", "vendor",
]);

function collectTechDebtFiles(directory, result = []) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (entry.isDirectory() && techDebtIgnoredDirectories.has(entry.name)) continue;
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      collectTechDebtFiles(path, result);
    } else if (techDebtExtensions.has(entry.name.slice(entry.name.lastIndexOf(".")).toLowerCase())) {
      result.push(path);
    }
  }
  return result;
}

function buildTechDebtStates(scanPath) {
  const absolutePath = resolve(scanPath);
  if (!existsSync(absolutePath) || !statSync(absolutePath).isDirectory()) {
    fail(`Tech-debt scan path is not a directory: ${scanPath}`);
  }

  const files = collectTechDebtFiles(absolutePath).sort();
  const fileSet = new Set(files.map((file) => file.toLowerCase()));
  return files.map((file) => {
    const source = readFileSync(file, "utf8");
    const lines = source.split(/\r?\n/);
    const packageMatch = source.match(/^package\s+([A-Za-z0-9_]+)/m);
    const todoMarkers = (source.match(/\b(?:TODO|FIXME|HACK|XXX)\b/gi) || []).length;
    const longLines = lines.filter((line) => line.length > 120).length;
    const relativePath = relative(root, file).replaceAll("\\", "/");
    const testFile = file.endsWith("_test.go");
    const siblingTest = testFile
      ? file.slice(0, -"_test.go".length) + ".go"
      : file.slice(0, -".go".length) + "_test.go";
    const ignoredErrors = lines.filter((line) => /\b_\s*=\s*[^=].*\berr\b/.test(line)).length;
    const imports = [...source.matchAll(/^\s*"([^"]+)"/gm)].map((match) => match[1]);
    const riskSignals = [];
    if (lines.length >= 300) riskSignals.push(`large file (${lines.length} lines)`);
    if (todoMarkers > 0) riskSignals.push(`${todoMarkers} TODO/FIXME/HACK marker(s)`);
    if (longLines >= 10) riskSignals.push(`${longLines} lines exceed 120 characters`);
    if (ignoredErrors > 0) riskSignals.push(`${ignoredErrors} line(s) appear to discard errors`);
    if (/\b(exec\.Command|os\.Exec|syscall\.)/.test(source)) riskSignals.push("process or system command execution");
    if (/\b(net\/http|http\.Handler|http\.Server|ListenAndServe)\b/.test(source)) riskSignals.push("HTTP/network boundary");
    if (/\b(crypto\/|bcrypt|sha1|sha256|AES|JWT|token|password|secret)\b/i.test(source)) riskSignals.push("security-sensitive code");
    if (/\b(sync\.Mutex|sync\.RWMutex|go\s+func|WaitGroup)\b/.test(source)) riskSignals.push("concurrency or shared state");
    if (/\b(SELECT|INSERT|UPDATE|DELETE)\b/i.test(source)) riskSignals.push("database access");
    if (testFile) riskSignals.push("test file");

    return {
      path: relativePath,
      package: packageMatch?.[1] || "unknown",
      source,
      lineCount: lines.length,
      testFile,
      hasSiblingTest: fileSet.has(siblingTest.toLowerCase()),
      todoCount: todoMarkers,
      longLineCount: longLines,
      ignoredErrorCount: ignoredErrors,
      imports,
      functionCount: (source.match(/^\s*func\s+/gm) || []).length,
      exportedFunctionCount: (source.match(/^\s*func\s+[A-Z][A-Za-z0-9_]*/gm) || []).length,
      riskSignals,
    };
  });
}

function techDebtQuestions() {
  return {
    fileRole: {
      type: "choice",
      instructions: "What role does this Go file play, using `path`, `testFile`, `package`, `imports`, and `source`?",
      criteria: {
        production: "Production application or domain code",
        test: "Automated test or test helper code",
        infrastructure: "Runtime, persistence, networking, deployment, or infrastructure code",
        security: "Authentication, authorization, cryptography, secrets, or security boundary code",
        generated: "Generated or mechanically produced code",
        configuration: "Configuration, wiring, or application bootstrap code",
        unknown: "The role cannot be determined reliably",
      },
    },
    hasActionableDebt: {
      type: "noul",
      instructions: "Does `source` contain a specific, actionable technical-debt problem rather than merely being nontrivial code?",
      criteria: { true: "A concrete problem can be named and acted on", false: "No concrete debt is supported by this file" },
    },
    debtType: {
      type: "choice",
      instructions: "What is the primary actionable technical-debt category in `source`? Use `riskSignals` and metrics as supporting evidence, not as proof.",
      criteria: {
        maintainability: "Hard to understand, modify, or review",
        architecture: "Structural, coupling, boundary, or dependency problem",
        testing: "Insufficient automated verification",
        reliability: "Likely to cause bugs, failure modes, or operational problems",
        security: "Creates a security or compliance exposure",
        performance: "Likely creates a meaningful runtime or scalability cost",
        dependency: "Problem caused by an outdated, risky, or inappropriate dependency",
        documentation: "Important behavior or design is inadequately documented",
        none: "No actionable technical debt is supported by the file",
      },
    },
    severity: {
      type: "score",
      instructions: "How severe is the actionable debt in this file if left unresolved? Consider user impact, data loss, security exposure, outages, and maintenance cost.",
      criteria: ["Negligible", "Low", "Moderate", "High", "Critical"],
    },
    maintenanceBurden: {
      type: "score",
      instructions: "How difficult is this Go file to understand, safely modify, and review? Judge `source`, not line count alone.",
      criteria: ["Easy", "Minor friction", "Noticeably difficult", "Risky to modify", "Extremely difficult"],
    },
    changeRisk: {
      type: "score",
      instructions: "How likely is changing this Go file to cause regressions because of its current design, hidden side effects, shared state, or weak tests?",
      criteria: ["Unlikely", "Low risk", "Moderate risk", "High risk", "Severe risk"],
    },
    testabilityRisk: {
      type: "score",
      instructions: "How much does this Go file's current design create a testing or verification risk, considering `testFile` and `hasSiblingTest`?",
      criteria: ["Well tested", "Minor gaps", "Meaningful gaps", "Poorly tested", "Effectively untestable"],
    },
    architecturalCoupling: {
      type: "score",
      instructions: "How tightly coupled or architecturally entangled is this Go file with other concerns, based on its imports and `source`?",
      criteria: ["Isolated", "Lightly coupled", "Moderately coupled", "Tightly coupled", "Entangled"],
    },
    securityConcern: {
      type: "noul",
      instructions: "Does `source` contain a concrete security concern such as broken access control, secret exposure, unsafe input handling, weak cryptography, injection, or denial of service?",
      criteria: { true: "A concrete security weakness is visible in this file", false: "No concrete security weakness is visible in this file" },
    },
    reliabilityConcern: {
      type: "noul",
      instructions: "Does `source` contain a concrete reliability concern such as swallowed errors, incorrect cleanup, race-prone state, resource leaks, or failure handling that can break production behavior?",
      criteria: { true: "A concrete production failure mode is visible in this file", false: "No concrete production failure mode is visible in this file" },
    },
    performanceConcern: {
      type: "noul",
      instructions: "Does `source` contain a concrete performance or scalability concern such as unbounded memory, repeated expensive work, blocking operations, or inefficient database access?",
      criteria: { true: "A concrete performance or scalability problem is visible in this file", false: "No concrete performance or scalability problem is visible in this file" },
    },
    duplicationConcern: {
      type: "noul",
      instructions: "Does `source` contain meaningful duplicated logic or duplicated knowledge that is likely to diverge?",
      criteria: { true: "Meaningful duplication is visible in this file", false: "No meaningful duplication is visible in this file" },
    },
    obsoleteCode: {
      type: "noul",
      instructions: "Does `source` contain code that appears obsolete, unreachable, or superseded?",
      criteria: { true: "Obsolete, unreachable, or superseded code is visible", false: "No such code is visible" },
    },
    debtWillSpread: {
      type: "noul",
      instructions: "Will leaving the actionable debt in this Go file unchanged likely increase future cost or risk?",
      criteria: { true: "Leaving it unchanged is likely to increase future cost or risk", false: "Leaving it unchanged is unlikely to increase future cost or risk" },
    },
    needsHumanReview: {
      type: "noul",
      instructions: "Is the evidence or judgment ambiguous enough that a human engineer should review this file before Janus creates an actionable debt item?",
      criteria: { true: "Human review is needed before creating work", false: "The evidence is sufficiently clear for automated triage" },
    },
  };
}

function scoreValue(answer) {
  return typeof answer?.score === "number" ? answer.score : 0;
}

function priorityForTechDebt(answers) {
  const actionable = answers.hasActionableDebt?.noul || 0;
  const severity = scoreValue(answers.severity) / 4;
  const maintenance = scoreValue(answers.maintenanceBurden) / 4;
  const changeRisk = scoreValue(answers.changeRisk) / 4;
  const testability = scoreValue(answers.testabilityRisk) / 4;
  const coupling = scoreValue(answers.architecturalCoupling) / 4;
  const spread = answers.debtWillSpread?.noul || 0;
  const security = answers.securityConcern?.noul || 0;
  const reliability = answers.reliabilityConcern?.noul || 0;
  const performance = answers.performanceConcern?.noul || 0;
  const confidenceValues = [
    answers.fileRole?.confidence,
    answers.hasActionableDebt?.confidence,
    answers.debtType?.confidence,
    answers.severity?.confidence,
    answers.changeRisk?.confidence,
  ].filter((value) => typeof value === "number");
  const confidence = confidenceValues.length ? Math.min(...confidenceValues) : 0;
  const priority = Number((100 * actionable * (
    (0.30 * severity) +
    (0.15 * changeRisk) +
    (0.15 * maintenance) +
    (0.10 * testability) +
    (0.10 * coupling) +
    (0.10 * spread) +
    (0.05 * security) +
    (0.05 * reliability) +
    (0.05 * performance)
  )).toFixed(2));
  const reviewRequired = confidence < 0.5 ||
    (answers.needsHumanReview?.noul || 0) >= 0.6 ||
    security >= 0.8 ||
    reliability >= 0.8;

  return {
    score: priority,
    confidence: Number(confidence.toFixed(3)),
    reviewRequired,
    confidenceBand: confidence >= 0.75 ? "high" : confidence >= 0.5 ? "medium" : "low",
    actionable,
    security,
    reliability,
    severity: scoreValue(answers.severity),
  };
}

function actionForTechDebt(answers, scoring) {
  if (scoring.actionable < 0.5 || answers.debtType?.choice === "none" || scoring.severity < 1) return "ignore";
  if (scoring.reviewRequired) return "human_review";
  if (scoring.severity >= 3 || scoring.security >= 0.8 || scoring.reliability >= 0.8) return "fix_now";
  if ((answers.debtWillSpread?.noul || 0) >= 0.65 || scoring.severity >= 2) return "schedule";
  return "monitor";
}

function nextStepForTechDebt(answers, state, action) {
  const category = answers.debtType?.choice;
  if (action === "human_review") return "Have an engineer confirm the finding against the surrounding package and runtime behavior before creating work.";
  if (action === "ignore") return "No immediate debt work; retain the file as a low-priority baseline unless new evidence appears.";
  if (category === "security") return "Review the security boundary, add a focused regression test, and document the threat model before changing behavior.";
  if (category === "testing") return state.testFile
    ? "Keep this test focused; identify the production behavior it protects and add missing failure-path coverage there."
    : "Add focused tests around the risky branches and failure paths before refactoring the implementation.";
  if (category === "reliability") return "Make failures observable and test cleanup, timeout, retry, and error paths before restructuring.";
  if (category === "architecture") return "Map the package boundary and extract one responsibility behind a small interface before a broader refactor.";
  if (category === "performance") return "Measure the suspected hot path or resource growth first, then change it with a regression benchmark or load test.";
  return "Create a small, test-backed refactoring task with a clear before/after success condition.";
}

function actionableInsight(state, answers, scoring, action) {
  const evidence = [...state.riskSignals];
  if (!state.testFile && !state.hasSiblingTest) evidence.push("no sibling test file detected");
  if (answers.securityConcern?.noul >= 0.7) evidence.push(`security signal ${(answers.securityConcern.noul * 100).toFixed(0)}%`);
  if (answers.reliabilityConcern?.noul >= 0.7) evidence.push(`reliability signal ${(answers.reliabilityConcern.noul * 100).toFixed(0)}%`);
  if (answers.debtWillSpread?.noul >= 0.7) evidence.push(`spread risk ${(answers.debtWillSpread.noul * 100).toFixed(0)}%`);
  return {
    file: state.path,
    category: answers.debtType?.choice || "unknown",
    severity: scoring.severity,
    priority: scoring.score,
    confidence: scoring.confidence,
    confidenceBand: scoring.confidenceBand,
    action,
    evidence: [...new Set(evidence)].slice(0, 6),
    nextStep: nextStepForTechDebt(answers, state, action),
  };
}

function summarizeTechDebtResults(results, repository) {
  const files = results
    .map(({ state, answers, error }) => {
      const scoring = error ? null : priorityForTechDebt(answers);
      const action = scoring ? actionForTechDebt(answers, scoring) : "human_review";
      return {
        file: state.path,
        package: state.package,
        lineCount: state.lineCount,
        testFile: state.testFile,
        answers,
        priority: scoring?.score ?? null,
        confidence: scoring?.confidence ?? null,
        confidenceBand: scoring?.confidenceBand || "low",
        reviewRequired: scoring ? scoring.reviewRequired : true,
        recommendedAction: action,
        insight: scoring ? actionableInsight(state, answers, scoring, action) : null,
        error: error || null,
      };
    })
    .sort((a, b) => (b.priority ?? -1) - (a.priority ?? -1));

  return {
    repository,
    analyzedFiles: files.filter((file) => !file.error).length,
    failedFiles: files.filter((file) => file.error).length,
    actionableFiles: files.filter((file) => file.insight?.action !== "ignore").length,
    humanReviewFiles: files.filter((file) => file.reviewRequired).length,
    files,
    questions: techDebtQuestions(),
    note: "Jev makes atomic judgments; Janus computes the action and priority. Low-confidence or security/reliability findings are routed to human review.",
  };
}

async function requestTechDebtAnalysis(state) {
  for (let attempt = 0; attempt < 3; attempt += 1) {
    const response = await fetch(process.env.TYPESAFE_API_URL || "https://api.typesafe.ai/v1/systemone", {
      method: "POST",
      headers: {
        Authorization: `Bearer ${process.env.TYPESAFE_API_KEY}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        model: process.env.TYPESAFE_MODEL || "jev-latest",
        state,
        questions: techDebtQuestions(),
      }),
      signal: AbortSignal.timeout(30_000),
    });

    const body = await response.json();
    if (response.ok) return body.answers || body;

    if ((response.status === 429 || response.status === 529) && attempt < 2) {
      const retryAfter = Number.parseInt(response.headers.get("retry-after") || "", 10);
      const delay = Number.isFinite(retryAfter) ? Math.min(retryAfter * 1000, 10_000) : 500 * (2 ** attempt);
      await new Promise((resolvePromise) => setTimeout(resolvePromise, delay));
      continue;
    }
    throw new Error(`Jev request failed (${response.status}): ${JSON.stringify(body)}`);
  }
}

async function runWithConcurrency(items, concurrency, worker) {
  const results = new Array(items.length);
  let nextIndex = 0;
  async function consume() {
    while (nextIndex < items.length) {
      const index = nextIndex;
      nextIndex += 1;
      results[index] = await worker(items[index], index);
    }
  }
  await Promise.all(Array.from({ length: Math.min(concurrency, items.length) }, consume));
  return results;
}

function printTechDebtHumanReport(report) {
  const counts = new Map();
  for (const file of report.files) counts.set(file.recommendedAction, (counts.get(file.recommendedAction) || 0) + 1);
  const actionable = report.files
    .filter((file) => file.insight && file.recommendedAction !== "ignore" && !file.testFile)
    .sort((a, b) => b.priority - a.priority)
    .slice(0, 12);
  const testFindings = report.files
    .filter((file) => file.insight && file.testFile && file.recommendedAction !== "ignore")
    .sort((a, b) => b.priority - a.priority)
    .slice(0, 5);

  console.log(`Tech-debt report: ${report.repository}`);
  console.log(`Analyzed ${report.analyzedFiles} Go files; ${report.failedFiles} failed.`);
  console.log(`Action queues: fix_now=${counts.get("fix_now") || 0}, schedule=${counts.get("schedule") || 0}, human_review=${counts.get("human_review") || 0}, monitor=${counts.get("monitor") || 0}, ignore=${counts.get("ignore") || 0}`);
  console.log(`Review required: ${report.humanReviewFiles}; actionable candidates: ${report.actionableFiles}`);

  if (actionable.length === 0) {
    console.log("\nNo production-file candidates passed the current triage rules.");
  } else {
    console.log("\nTop actionable production findings:");
    actionable.forEach((file, index) => {
      const insight = file.insight;
      console.log(`\n${index + 1}. ${insight.file} [${insight.category}, severity ${insight.severity}/4, priority ${insight.priority}/100]`);
      console.log(`   Action: ${insight.action}; confidence: ${insight.confidenceBand} (${insight.confidence.toFixed(2)})`);
      console.log(`   Evidence: ${insight.evidence.join("; ") || "no local signal; semantic review only"}`);
      console.log(`   Next: ${insight.nextStep}`);
    });
  }

  if (testFindings.length) {
    console.log("\nTest-file follow-ups:");
    testFindings.forEach((file) => console.log(`- ${file.file}: ${file.insight.action}, priority ${file.insight.priority}/100 — ${file.insight.nextStep}`));
  }

  console.log("\nNote: security, reliability, low-confidence, and ambiguous findings are intentionally routed to human review. Use --json for complete answers and probabilities.");
}

function printTechDebtReport(report, jsonOutput) {
  if (jsonOutput) {
    console.log(JSON.stringify(report, null, 2));
    return;
  }
  printTechDebtHumanReport(report);
}

async function runTechDebt(taskArgs) {
  const scanPath = taskArgs.find((arg) => !arg.startsWith("--")) || ".";
  const dryRun = taskArgs.includes("--dry-run");
  const jsonOutput = taskArgs.includes("--json");
  const states = buildTechDebtStates(scanPath);
  const repository = relative(root, resolve(scanPath)) || ".";

  if (dryRun) {
    printTechDebtReport({
      mode: "dry-run",
      repository,
      files: states.map(({ source, ...metadata }) => metadata),
      questions: techDebtQuestions(),
    }, true);
    return;
  }

  if (!process.env.TYPESAFE_API_KEY) {
    fail("TYPESAFE_API_KEY is not set. Use --dry-run to inspect the local evidence without calling Jev.");
  }

  const concurrency = Number.parseInt(process.env.TYPESAFE_CONCURRENCY || "4", 10);
  if (!Number.isInteger(concurrency) || concurrency < 1) {
    fail("TYPESAFE_CONCURRENCY must be a positive integer.");
  }

  const results = await runWithConcurrency(states, concurrency, async (state) => {
    try {
      return { state, answers: await requestTechDebtAnalysis(state) };
    } catch (error) {
      return { state, answers: {}, error: error.message };
    }
  });
  printTechDebtReport(summarizeTechDebtResults(results, repository), jsonOutput);
}

async function generateGodoc() {
  const packages = new Map();
  const files = [];
  walk(backendDir, files);

  for (const file of files.filter((item) => item.endsWith(".go") && !item.endsWith("_test.go"))) {
    const source = readFileSync(file, "utf8");
    const packageMatch = source.match(/^package\s+([A-Za-z0-9_]+)/m);
    if (!packageMatch) continue;
    const packageDir = relative(backendDir, file).split(/[\\/]/).slice(0, -1).join("/") || ".";
    const key = `${packageDir}|${packageMatch[1]}`;
    if (!packages.has(key)) packages.set(key, { dir: packageDir, name: packageMatch[1], files: [] });
    const entry = packages.get(key);
    const lines = source.split(/\r?\n/);
    const functions = [];
    const packageLine = lines.findIndex((line) => /^package\s+/.test(line));
    const packageComments = linesBeforePackage(lines, packageLine);
    for (let index = 0; index < lines.length; index += 1) {
      const match = lines[index].match(/^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*(\(.*)$/);
      if (!match) continue;
      functions.push({
        name: match[1],
        signature: lines[index].trim(),
        description: docComment(lines, index) || `Implementation function ${match[1]}.`,
        line: index + 1,
      });
    }
    entry.files.push({ path: relative(root, file).replaceAll("\\", "/"), functions, packageComments });
  }

  const output = [
    "# Janus Go API Reference",
    "",
    "> Generated by `bun Janus.js godoc`. This reference covers production Go files and excludes tests.",
    "",
  ];

  for (const entry of [...packages.values()].sort((a, b) => a.dir.localeCompare(b.dir))) {
    const packageDescription = entry.files.find((file) => file.packageComments)?.packageComments || `Package ${entry.name}.`;
    output.push(`## \`${entry.dir === "." ? "cmd" : entry.dir}\``, "", `Package: \`${entry.name}\``, "", packageDescription, "");
    for (const file of entry.files.sort((a, b) => a.path.localeCompare(b.path))) {
      output.push(`### [${file.path}](../${file.path})`, "");
      if (file.functions.length === 0) {
        output.push("No top-level functions.", "");
        continue;
      }
      for (const fn of file.functions) {
        output.push(`- \`${fn.signature}\` — line ${fn.line}`, `  ${fn.description}`);
      }
      output.push("");
    }
  }

  const outputPath = join(root, "docs", "godoc.md");
  await Bun.write(outputPath, `${output.join("\n")}\n`);
  log(`generated ${relative(root, outputPath)}`);
}

function linesBeforePackage(lines, packageIndex) {
  for (let index = packageIndex - 1; index >= 0; index -= 1) {
    if (lines[index].trim() === "") continue;
    if (!lines[index].trim().startsWith("//")) break;
    const comments = [];
    for (let cursor = index; cursor >= 0 && lines[cursor].trim().startsWith("//"); cursor -= 1) {
      comments.unshift(lines[cursor].replace(/^\s*\/\/\s?/, "").trim());
    }
    return comments.join(" ");
  }
  return "";
}

function docComment(lines, declarationIndex) {
  const comments = [];
  for (let index = declarationIndex - 1; index >= 0; index -= 1) {
    const line = lines[index].trim();
    if (!line) {
      if (comments.length > 0) break;
      continue;
    }
    if (!line.startsWith("//")) break;
    comments.unshift(line.replace(/^\/\/\s?/, "").trim());
  }
  return comments.join(" ");
}

function checkHexagonal() {
  const forbidden = [
    ["internal/core", "legacy internal/core import"],
    ["core\\.State", "legacy core.State reference"],
    ["core\\.DataStore", "legacy core.DataStore reference"],
    ["core\\.NewState", "legacy core.NewState reference"],
    ["SetExecutionBackends", "legacy SetExecutionBackends reference"],
  ];
  const files = [];
  walk(backendDir, files);
  let failed = false;
  for (const file of files.filter((item) => item.endsWith(".go") && !item.endsWith("_test.go"))) {
    const lines = readFileSync(file, "utf8").split(/\r?\n/);
    for (const [pattern, label] of forbidden) {
      lines.forEach((line, index) => {
        if (new RegExp(pattern).test(line)) {
          failed = true;
          console.error(`FAILED: ${label}\n  ${relative(root, file)}:${index + 1}: ${line.trim()}`);
        }
      });
    }
  }
  if (failed) process.exit(1);
  log("hexagonal boundary check passed");
}

function walk(directory, result) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) walk(path, result);
    else result.push(path);
  }
}

async function runTask(name, taskArgs) {
  switch (name) {
    case "help":
    case "--help":
    case "-h":
      printHelp();
      return;
    case "godoc":
      await generateGodoc();
      return;
    case "tech-debt":
    case "techdebt":
      await runTechDebt(taskArgs);
      return;
    case "build":
      build();
      return;
    case "operator":
    case "cli":
      cargo(["run", "--manifest-path", rustManifest, "--bin", "janus", "--", ...taskArgs], { cwd: root });
      return;
    case "run":
    case "backend-run":
      runBackend(taskArgs);
      return;
    case "dev":
      await runDevelopment();
      return;
    case "backend-worker":
      fail("The Rust API owns the bounded local worker boundary; run `bun Janus.js run`.");
      return;
    case "backend-migrate":
      fail("PostgreSQL migrations were replaced by the Rust SpacetimeDB module; run the production persistence gate.");
      return;
    case "backend-up":
      compose(["up", "-d", "--build", "--force-recreate", ...backendServices()]);
      return;
    case "backend-down":
      compose(["rm", "-f", "-s", ...backendServices()]);
      return;
    case "backend-logs":
      compose(["logs", "-f", ...(taskArgs.length ? taskArgs : backendServices())]);
      return;
    case "backend-ps":
      compose(["ps", ...backendServices()]);
      return;
    case "stack-up":
      compose(["up", "-d", "--build", "--force-recreate", "--remove-orphans", ...backendServices()]);
      return;
    case "stack-up-build":
      compose(["build", ...backendServices()]);
      compose(["up", "-d", "--force-recreate", "--remove-orphans", ...backendServices()]);
      return;
    case "stack-rebuild":
      compose(["build", "--no-cache", ...backendServices()]);
      compose(["up", "-d", "--force-recreate", "--remove-orphans", ...backendServices()]);
      return;
    case "stack-down":
      compose(["down", "--remove-orphans"]);
      return;
    case "stack-logs":
      compose(["logs", "-f", ...taskArgs]);
      return;
    case "stack-ps":
      compose(["ps"]);
      return;
    case "backend-test":
    case "backend-tests":
      cargo(["test", "--manifest-path", rustManifest, "--all-targets", "--all-features"], { cwd: root });
      return;
    case "backend-build":
      cargo(["build", "--manifest-path", rustManifest, "--all-targets", "--all-features"], { cwd: root });
      return;
    case "web-dev":
      await runDevelopment();
      return;
    case "web-build":
      build();
      return;
    default:
      fail(`Unknown command '${name}'. Run 'bun Janus.js help' for available commands.`);
  }
}

try {
  await runTask(commandName, args);
} catch (error) {
  console.error(`[janus] ${error.message}`);
  process.exit(1);
}
