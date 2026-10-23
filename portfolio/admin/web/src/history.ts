import type { LiveSnapshot } from "./types";

/** Samples kept per series. Frames arrive every few seconds, so this is a few minutes of history. */
export const HISTORY_LIMIT = 100;

/** Series keys. Container keys carry the container id after the colon. */
export const SERIES = {
  cpu: "cpu",
  netDown: "net:down",
  netUp: "net:up",
  container: (id: string) => `ctr:${id}`,
  containerDown: (id: string) => `ctr-down:${id}`,
  containerUp: (id: string) => `ctr-up:${id}`,
};

export interface History {
  series: Record<string, number[]>;
  // Last container byte counters, kept to turn running totals into rates.
  counters: Record<string, { rx: number; tx: number; at: number }>;
}

export function newHistory(): History {
  return { series: {}, counters: {} };
}

function push(h: History, key: string, value: number) {
  const s = h.series[key] ?? (h.series[key] = []);
  s.push(value);
  if (s.length > HISTORY_LIMIT) s.splice(0, s.length - HISTORY_LIMIT);
}

/**
 * Adds one frame to the history. Only the topics a frame carries are recorded,
 * because the server sends just the topics that changed.
 */
export function recordFrame(h: History, data: Partial<LiveSnapshot> & { collectedAt: string }) {
  if (data.cpu) push(h, SERIES.cpu, data.cpu.totalPercent);

  if (data.network) {
    const ifaces = data.network.interfaces ?? [];
    push(h, SERIES.netDown, ifaces.reduce((a, i) => a + i.recvRate, 0));
    push(h, SERIES.netUp, ifaces.reduce((a, i) => a + i.sendRate, 0));
  }

  if (data.docker?.containers) {
    const at = Date.parse(data.collectedAt);
    for (const c of data.docker.containers) {
      push(h, SERIES.container(c.id), c.cpuPercent);
      const prev = h.counters[c.id];
      if (prev && at > prev.at) {
        const seconds = (at - prev.at) / 1000;
        // A counter that goes down means the container restarted, so no rate for that step.
        push(h, SERIES.containerDown(c.id), Math.max(0, c.netRx - prev.rx) / seconds);
        push(h, SERIES.containerUp(c.id), Math.max(0, c.netTx - prev.tx) / seconds);
      }
      h.counters[c.id] = { rx: c.netRx, tx: c.netTx, at };
    }
  }
}

/** The newest value of a series, or 0 when nothing has been recorded yet. */
export function last(series: number[] | undefined): number {
  return series && series.length ? series[series.length - 1] : 0;
}
