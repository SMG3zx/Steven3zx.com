import { resolve } from "node:path";
import { createServer } from "node:net";

const root = resolve(import.meta.dir, "..");
const mastraRoot = resolve(root, "mastra");

async function isPortAvailable(port: number) {
  return await new Promise<boolean>((resolve) => {
    const probe = createServer();
    probe.once("error", () => resolve(false));
    // Both child services bind to 127.0.0.1, so probe that exact address.
    // On Windows a wildcard probe can incorrectly coexist with a loopback
    // listener and report a port as available when it is not.
    probe.listen(port, "127.0.0.1", () => {
      probe.close(() => resolve(true));
    });
  });
}

async function findAvailablePort(start: number) {
  for (let port = start; port <= 65535; port++) {
    if (await isPortAvailable(port)) return port;
  }
  throw new Error(`No available port found starting at ${start}`);
}

const requestedUiPort = Number(process.env.UI_PORT ?? 5173);
const requestedMastraPort = Number(process.env.MASTRA_PORT ?? process.env.PORT ?? 4111);
const uiPort = await findAvailablePort(requestedUiPort);
const mastraPort = await findAvailablePort(requestedMastraPort);
const factoryApiUrl = `http://127.0.0.1:${mastraPort}`;
const uiOrigin = `http://127.0.0.1:${uiPort}`;

console.log(`MeinFactory: Qwik ${uiPort}, Mastra ${mastraPort}`);

const ui = Bun.spawn(["bun", "run", "dev:ui", "--", "--host", "127.0.0.1", "--port", String(uiPort)], {
  cwd: root,
  env: { ...process.env, UI_PORT: String(uiPort), VITE_FACTORY_API_URL: factoryApiUrl },
  stdin: "inherit",
  stdout: "inherit",
  stderr: "inherit",
  windowsHide: false,
});

const mastra = Bun.spawn(["bun", "run", "src/server.ts"], {
  cwd: mastraRoot,
  env: { ...process.env, PORT: String(mastraPort), UI_ORIGIN: uiOrigin },
  stdin: "inherit",
  stdout: "inherit",
  stderr: "inherit",
  windowsHide: false,
});

let shuttingDown = false;
const shutdown = (reason: string) => {
  if (shuttingDown) return;
  shuttingDown = true;
  console.log(`\nMeinFactory: stopping services (${reason})`);
  if (!ui.killed) ui.kill("SIGTERM");
  if (!mastra.killed) mastra.kill("SIGTERM");
};

process.on("SIGINT", () => shutdown("interrupt"));
process.on("SIGTERM", () => shutdown("termination"));

const firstExit = await Promise.race([
  ui.exited.then((code) => ({ name: "Qwik", code })),
  mastra.exited.then((code) => ({ name: "Mastra", code })),
]);

if (!shuttingDown) shutdown(`${firstExit.name} exited with code ${firstExit.code}`);
await Promise.all([ui.exited, mastra.exited]);
process.exit(firstExit.code ?? 1);
