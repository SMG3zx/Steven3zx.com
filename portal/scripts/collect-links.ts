/**
 * Builds `src/generated-links.json` from two sources:
 *
 *   1. `links.manual.json` in this folder, for links written by hand.
 *   2. Every tracked `portal.json` in the repository. A project lists its own links
 *      by adding that file at its root: an array of `{ title, url, description?, tags? }`.
 *
 * Only git-tracked files are read, so ignored and untracked files never leak in.
 * `cold-storage/` folders are skipped, as AGENTS.md requires.
 *
 * Usage: bun run links
 */
import { join, basename, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { slugOf } from "../src/slug";

interface SavedLink {
  title: string;
  url: string;
  description?: string;
  tags: string[];
}

const portalDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const repoDir = join(portalDir, "..");
const outPath = join(portalDir, "src", "generated-links.json");

function fail(source: string, message: string): never {
  throw new Error(`${source}: ${message}`);
}

function parseLink(raw: unknown, source: string, index: number): SavedLink {
  const where = `${source} entry ${index + 1}`;
  if (typeof raw !== "object" || raw === null) {
    fail(where, "must be an object");
  }
  const { title, url, description, tags } = raw as Record<string, unknown>;

  if (typeof title !== "string" || title.trim() === "") {
    fail(where, 'needs a non-empty "title"');
  }
  if (typeof url !== "string") {
    fail(where, 'needs a "url"');
  }
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
      fail(where, `"${url}" must be http or https`);
    }
  } catch {
    fail(where, `"${url}" is not a valid URL`);
  }
  if (description !== undefined && typeof description !== "string") {
    fail(where, '"description" must be a string');
  }
  if (
    tags !== undefined &&
    (!Array.isArray(tags) || tags.some((t) => typeof t !== "string"))
  ) {
    fail(where, '"tags" must be an array of strings');
  }

  return {
    title: title.trim(),
    url,
    ...(description ? { description } : {}),
    tags: (tags as string[] | undefined) ?? [],
  };
}

async function readArray(path: string, source: string): Promise<unknown[]> {
  const data: unknown = await Bun.file(path).json();
  if (!Array.isArray(data)) {
    fail(source, "must contain a JSON array of links");
  }
  return data;
}

async function trackedPortalFiles(): Promise<string[]> {
  const proc = Bun.spawn(["git", "ls-files", "-z", "--", "*portal.json"], {
    cwd: repoDir,
    stdout: "pipe",
    stderr: "pipe",
  });
  const out = await new Response(proc.stdout).text();
  if ((await proc.exited) !== 0) {
    fail("git", "could not list tracked files");
  }
  return out
    .split("\0")
    .filter(Boolean)
    .filter((path) => basename(path) === "portal.json")
    .filter((path) => !path.split("/").includes("cold-storage"))
    .sort();
}

const links: SavedLink[] = [];

const manual = await readArray(
  join(portalDir, "links.manual.json"),
  "links.manual.json",
);
manual.forEach((raw, i) => links.push(parseLink(raw, "links.manual.json", i)));

for (const file of await trackedPortalFiles()) {
  const entries = await readArray(join(repoDir, file), file);
  entries.forEach((raw, i) => links.push(parseLink(raw, file, i)));
}

// Preview images are named after the title, so two titles that slugify the same would clash.
const seenUrls = new Map<string, string>();
const seenSlugs = new Map<string, string>();
const unique: SavedLink[] = [];

for (const link of links) {
  const slug = slugOf(link.title);
  if (seenUrls.has(link.url)) {
    console.warn(`skip   duplicate url ${link.url} ("${link.title}")`);
    continue;
  }
  if (seenSlugs.has(slug)) {
    fail(
      link.title,
      `title clashes with "${seenSlugs.get(slug)}" in the preview file name "${slug}"`,
    );
  }
  seenUrls.set(link.url, link.title);
  seenSlugs.set(slug, link.title);
  unique.push(link);
}

await Bun.write(outPath, JSON.stringify(unique, null, 2) + "\n");
console.log(`wrote ${unique.length} links to src/generated-links.json`);
