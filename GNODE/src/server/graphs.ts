// Saved graphs live as JSON files in graphs/. Names are restricted to a
// small safe set so a request can never reach outside that folder.

import { readdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { parseGraph, type Graph } from "../shared/graph";
import { ROOT } from "./go";

export const GRAPHS_DIR = resolve(ROOT, "graphs");

const NAME = /^[a-z0-9][a-z0-9-]{0,62}$/;

export function safeName(name: string): string {
  if (!NAME.test(name)) throw new Error("names use lowercase letters, digits and dashes");
  return name;
}

export async function listGraphs(): Promise<string[]> {
  const files = await readdir(GRAPHS_DIR).catch(() => [] as string[]);
  return files.filter((f) => f.endsWith(".json")).map((f) => f.slice(0, -5)).sort();
}

export async function readGraph(name: string): Promise<Graph> {
  const text = await readFile(join(GRAPHS_DIR, safeName(name) + ".json"), "utf8");
  return parseGraph(JSON.parse(text));
}

export async function writeGraph(name: string, graph: Graph): Promise<void> {
  const checked = parseGraph(graph);
  await writeFile(join(GRAPHS_DIR, safeName(name) + ".json"), JSON.stringify(checked, null, 2) + "\n");
}
