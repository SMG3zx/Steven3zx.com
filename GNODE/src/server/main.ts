// The local server: serves the editor and a small JSON API. It binds to
// 127.0.0.1 only, because the run action executes the generated program.

import { join } from "node:path";
import { generate } from "../codegen/generate";
import { parseGraph } from "../shared/graph";
import { formatSource, runProgram, ROOT } from "./go";
import { listGraphs, readGraph, safeName, writeGraph } from "./graphs";

const PORT = Number(Bun.env.PORT ?? 4830);
const WEB_DIR = join(ROOT, "dist", "web");

function json(body: unknown, status = 200): Response {
  return Response.json(body, { status });
}

function errorResponse(err: unknown, status = 400): Response {
  return json({ error: (err as Error).message }, status);
}

async function readJson(req: Request): Promise<unknown> {
  try {
    return await req.json();
  } catch {
    throw new Error("request body must be JSON");
  }
}

async function generateFor(body: unknown) {
  const graph = parseGraph((body as { graph?: unknown })?.graph);
  const result = generate(graph);
  const formatted = await formatSource(result.source);
  return { graph, result, formatted };
}

const server = Bun.serve({
  hostname: "127.0.0.1",
  port: PORT,
  routes: {
    "/": async () => serveFile(join(WEB_DIR, "index.html")),
    "/app.js": async () => serveFile(join(WEB_DIR, "app.js")),
    "/styles.css": async () => serveFile(join(WEB_DIR, "styles.css")),
    "/app.js.map": async () => serveFile(join(WEB_DIR, "app.js.map")),

    "/api/generate": {
      POST: async (req) => {
        try {
          const { result, formatted } = await generateFor(await readJson(req));
          return json({
            source: formatted.source,
            formatted: formatted.formatted,
            formatError: formatted.error,
            diagnostics: result.diagnostics,
            emittedNodeIds: result.emittedNodeIds,
          });
        } catch (err) {
          return errorResponse(err);
        }
      },
    },

    "/api/run": {
      POST: async (req) => {
        try {
          const { graph, result, formatted } = await generateFor(await readJson(req));
          const errors = result.diagnostics.filter((d) => d.level === "error");
          if (errors.length > 0) return json({ ok: false, diagnostics: result.diagnostics, output: "" }, 422);
          const run = await runProgram(safeName(graph.name.toLowerCase().replace(/[^a-z0-9-]+/g, "-") || "program"), formatted.source);
          return json({ ...run, diagnostics: result.diagnostics, source: formatted.source });
        } catch (err) {
          return errorResponse(err);
        }
      },
    },

    "/api/graphs": {
      GET: async () => json({ graphs: await listGraphs() }),
    },

    "/api/graphs/:name": {
      GET: async (req) => {
        try {
          return json(await readGraph(req.params.name));
        } catch (err) {
          return errorResponse(err, 404);
        }
      },
      PUT: async (req) => {
        try {
          await writeGraph(req.params.name, parseGraph(await readJson(req)));
          return json({ saved: req.params.name });
        } catch (err) {
          return errorResponse(err);
        }
      },
    },
  },
  fetch() {
    return json({ error: "not found" }, 404);
  },
});

async function serveFile(path: string): Promise<Response> {
  const file = Bun.file(path);
  if (!(await file.exists())) return new Response("Build the web app first: bun run build", { status: 503 });
  return new Response(file);
}

console.log(`GNODE editor at http://127.0.0.1:${server.port}`);
