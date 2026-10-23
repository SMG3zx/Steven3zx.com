// Bundles the browser editor into dist/web and copies its static files.

import { cp, mkdir, rm } from "node:fs/promises";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const out = join(root, "dist", "web");

await rm(out, { recursive: true, force: true });
await mkdir(out, { recursive: true });

const result = await Bun.build({
  entrypoints: [join(root, "src", "web", "app.ts")],
  outdir: out,
  target: "browser",
  format: "esm",
  minify: false,
  sourcemap: "linked",
  naming: "[name].js",
});

if (!result.success) {
  for (const log of result.logs) console.error(log);
  process.exit(1);
}

await cp(join(root, "src", "web", "index.html"), join(out, "index.html"));
await cp(join(root, "src", "web", "styles.css"), join(out, "styles.css"));
console.log(`built editor into ${out}`);
