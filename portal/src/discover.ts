/**
 * Finds web pages served on this machine, so a local tool shows up in the portal
 * without anyone writing a link for it.
 *
 * It reads the listening TCP ports from /proc, asks each one for its front page, and
 * keeps those that answer with HTML. The page's <title> becomes the link title.
 * Server-only: it runs inside the route loader, never in the browser.
 */
import { readFile } from "node:fs/promises";
import type { SavedLink } from "./links";

/** How long one scan is reused before the next request starts a new one. */
const CACHE_MS = 30_000;
const PROBE_TIMEOUT_MS = 1_500;
/** A title is all that is needed, and it sits near the top of the page. */
const MAX_HTML_BYTES = 64 * 1024;
/** Socket state "listening" in /proc/net/tcp. */
const LISTEN = "0A";

/** Addresses a browser on this machine reaches as localhost: loopback and "any". */
const LOCAL_ADDRESSES = new Set([
  "0100007F", // 127.0.0.1
  "00000000", // 0.0.0.0
  "00000000000000000000000001000000", // ::1
  "00000000000000000000000000000000", // ::
]);

/** Ports listening on localhost, from the text of /proc/net/tcp or /proc/net/tcp6. */
export function listeningPorts(procNetTcp: string): number[] {
  const ports = new Set<number>();
  for (const line of procNetTcp.split("\n").slice(1)) {
    const fields = line.trim().split(/\s+/);
    if (fields[3] !== LISTEN) continue;
    const [address, port] = fields[1].split(":");
    if (LOCAL_ADDRESSES.has(address)) ports.add(parseInt(port, 16));
  }
  return [...ports].sort((a, b) => a - b);
}

/** The text of the first <title>, with entities decoded and whitespace collapsed. */
export function titleOf(html: string): string | undefined {
  const match = /<title[^>]*>([^<]*)<\/title>/i.exec(html);
  const title = match?.[1]
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#(\d+);/g, (_, code) => String.fromCodePoint(Number(code)))
    .replace(/\s+/g, " ")
    .trim();
  return title || undefined;
}

/** The port a saved link points at on this machine, if it points here at all. */
export function localPortOf(url: string): number | undefined {
  try {
    const { hostname, port, protocol } = new URL(url);
    if (!["localhost", "127.0.0.1", "[::1]"].includes(hostname))
      return undefined;
    return Number(port) || (protocol === "https:" ? 443 : 80);
  } catch {
    return undefined;
  }
}

async function readPorts(): Promise<number[]> {
  const tables = await Promise.all(
    ["/proc/net/tcp", "/proc/net/tcp6"].map((path) =>
      readFile(path, "utf8").catch(() => ""),
    ),
  );
  return [...new Set(tables.flatMap(listeningPorts))].sort((a, b) => a - b);
}

async function probe(port: number): Promise<SavedLink | undefined> {
  const url = `http://localhost:${port}`;
  try {
    const res = await fetch(`http://127.0.0.1:${port}/`, {
      redirect: "follow",
      signal: AbortSignal.timeout(PROBE_TIMEOUT_MS),
    });
    if (!res.ok || !res.headers.get("content-type")?.includes("text/html")) {
      await res.body?.cancel();
      return undefined;
    }
    const html = (await res.text()).slice(0, MAX_HTML_BYTES);
    return {
      title: titleOf(html) ?? `localhost:${port}`,
      url,
      description: `Found running on port ${port}.`,
      tags: ["local"],
      discovered: true,
    };
  } catch {
    return undefined;
  }
}

let cache: { at: number; links: SavedLink[] } | undefined;
let scanning: Promise<SavedLink[]> | undefined;

async function scan(): Promise<SavedLink[]> {
  const found = await Promise.all((await readPorts()).map(probe));
  const links = found.flatMap((link) => (link ? [link] : []));
  cache = { at: Date.now(), links };
  return links;
}

/**
 * Web pages served on this machine, apart from the ports in `skip` (the portal
 * itself, and anything a saved link already covers).
 *
 * Only the first request waits for a scan. After that the last result is returned
 * at once, and a stale one is refreshed in the background for the next request,
 * so one slow port never holds up the page.
 */
export async function discoverLocal(skip: number[]): Promise<SavedLink[]> {
  if (!cache || Date.now() - cache.at >= CACHE_MS) {
    scanning ??= scan().finally(() => (scanning = undefined));
  }
  const links = cache?.links ?? (await scanning!);
  return links.filter((link) => !skip.includes(localPortOf(link.url)!));
}
