/**
 * Screenshots every link in `src/generated-links.json` into `public/previews/<slug>.png`
 * and records the captured slugs in `src/previews.json`.
 *
 * Usage: bun run previews            (skips links that already have a preview)
 *        bun run previews -- --force (recaptures everything)
 *
 * Uses the system Chromium, so no extra npm dependency is needed.
 */
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import links from "../src/generated-links.json";
import { slugOf } from "../src/slug";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const previewDir = join(root, "public", "previews");
const manifestPath = join(root, "src", "previews.json");
const chromium = process.env.CHROMIUM ?? "/usr/bin/chromium";
const force = process.argv.includes("--force");

mkdirSync(previewDir, { recursive: true });

/** A site counts as live only if it answers with a success status. Error pages get no screenshot. */
async function isLive(url: string): Promise<boolean> {
  try {
    const res = await fetch(url, {
      redirect: "follow",
      signal: AbortSignal.timeout(15_000),
    });
    return res.ok;
  } catch {
    return false;
  }
}

const captured = new Set<string>();

for (const link of links) {
  const slug = slugOf(link.title);
  const out = join(previewDir, `${slug}.png`);

  if (!force && existsSync(out)) {
    captured.add(slug);
    console.log(`skip   ${link.title}`);
    continue;
  }

  if (!(await isLive(link.url))) {
    console.log(
      `offline ${link.title} (${link.url}), placeholder will be shown`,
    );
    continue;
  }

  const proc = Bun.spawn(
    [
      chromium,
      "--headless=new",
      "--no-sandbox",
      "--disable-gpu",
      "--hide-scrollbars",
      "--window-size=1200,750",
      "--virtual-time-budget=8000",
      `--screenshot=${out}`,
      link.url,
    ],
    { stdout: "ignore", stderr: "ignore" },
  );

  const timer = setTimeout(() => proc.kill(), 60_000);
  await proc.exited;
  clearTimeout(timer);

  if (existsSync(out)) {
    captured.add(slug);
    console.log(`ok     ${link.title}`);
  } else {
    console.log(`failed ${link.title} (${link.url})`);
  }
}

writeFileSync(
  manifestPath,
  JSON.stringify([...captured].sort(), null, 2) + "\n",
);
console.log(`\n${captured.size} of ${links.length} previews available`);
