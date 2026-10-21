// Saved graphs live as JSON files in graphs/. Names are restricted to a
// small safe set so a request can never reach outside that folder.
//
// Before a save overwrites a different version, the old file is copied to
// graphs/backups/<name>/, and only the newest few copies are kept.

import { mkdir, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { parseGraph, type Graph } from "../shared/graph";
import { ROOT } from "./go";

export const GRAPHS_DIR = resolve(ROOT, "graphs");
// Block groups saved from a selection. A group is stored in the same format as
// a program, with its blocks placed from the top left corner.
export const GROUPS_DIR = resolve(ROOT, "groups");

const NAME = /^[a-z0-9][a-z0-9-]{0,62}$/;
const BACKUPS_KEPT = 20;

export function safeName(name: string): string {
  if (!NAME.test(name)) throw new Error("names use lowercase letters, digits and dashes");
  return name;
}

export async function listGraphsIn(dir: string): Promise<string[]> {
  const files = await readdir(dir).catch(() => [] as string[]);
  return files.filter((f) => f.endsWith(".json")).map((f) => f.slice(0, -5)).sort();
}

export async function readGraphIn(dir: string, name: string): Promise<Graph> {
  const text = await readFile(join(dir, safeName(name) + ".json"), "utf8");
  return parseGraph(JSON.parse(text));
}

export async function listGraphs(): Promise<string[]> {
  return listGraphsIn(GRAPHS_DIR);
}

export async function readGraph(name: string): Promise<Graph> {
  return readGraphIn(GRAPHS_DIR, name);
}

export async function writeGraph(name: string, graph: Graph): Promise<void> {
  await saveGraphIn(GRAPHS_DIR, name, graph);
}

// Writes the graph into dir. A changed version of an existing program is
// copied to backups first. Saving the same text again makes no backup.
export async function saveGraphIn(dir: string, name: string, graph: Graph): Promise<void> {
  const checked = parseGraph(graph);
  const file = join(dir, safeName(name) + ".json");
  const text = JSON.stringify(checked, null, 2) + "\n";
  const previous = await readFile(file, "utf8").catch(() => null);
  if (previous !== null && previous !== text) await backUp(dir, name, previous);
  await mkdir(dir, { recursive: true });
  await writeFile(file, text);
}

async function backUp(dir: string, name: string, previous: string): Promise<void> {
  const folder = join(dir, "backups", name);
  await mkdir(folder, { recursive: true });
  // ISO time with ':' and '.' swapped out, so it is a valid file name and sorts by age.
  const stamp = new Date().toISOString().replace(/[:.]/g, "-");
  await writeFile(join(folder, `${stamp}.json`), previous);
  const old = (await readdir(folder)).sort().reverse().slice(BACKUPS_KEPT);
  await Promise.all(old.map((f) => rm(join(folder, f))));
}
