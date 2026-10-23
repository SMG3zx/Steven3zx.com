import { ApiError } from "./api";

/** One run an Agent reported, as the gateway lists it. Matches the gateway's JSON. */
export type AgentRun = {
  run_id: string;
  deployment_id: string;
  status: string;
  error?: string;
  /** The `GET /` run that decides whether a Deployment is healthy. */
  probe: boolean;
  started_at: string;
  fuel: number;
  peak_memory_bytes: number;
  duration_ms: number;
  cpu_time_us: number;
  network_bytes_in: number;
  network_bytes_out: number;
};

export type RunStatus = { label: string; tone: "ok" | "bad" | "muted" };

/** How a run ended. A status this page does not know is shown as the Agent sent it. */
export function runStatus(run: AgentRun): RunStatus {
  switch (run.status) {
    case "ok":
      return { label: "OK", tone: "ok" };
    case "failed":
      return { label: run.error ? `Failed: ${run.error}` : "Failed", tone: "bad" };
    case "out_of_fuel":
      return { label: "Out of fuel", tone: "bad" };
    case "timeout":
      return { label: "Timed out", tone: "bad" };
    default:
      return { label: run.status, tone: "muted" };
  }
}

/** The start time as `2026-10-23 10:00:05 UTC`. Server-rendered, so it must not depend on a locale. */
export function formatStarted(startedAt: string): string {
  const match = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})/.exec(startedAt);
  return match ? `${match[1]} ${match[2]} UTC` : startedAt;
}

export function formatDuration(milliseconds: number): string {
  if (milliseconds < 1000) {
    return `${milliseconds} ms`;
  }
  if (milliseconds < 60_000) {
    return `${(milliseconds / 1000).toFixed(1)} s`;
  }
  const minutes = Math.floor(milliseconds / 60_000);
  const seconds = Math.floor((milliseconds % 60_000) / 1000);
  return `${minutes} min ${seconds} s`;
}

const byteUnits = ["KiB", "MiB", "GiB"];

export function formatBytes(bytes: number): string {
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < byteUnits.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(1)} ${byteUnits[unit]}`;
}

type Gateway = {
  get<T>(path: string, options: { cookie?: string }): Promise<T>;
};

/**
 * What the run history page can show: the runs, or why there are none. The
 * gateway answers 404 for an Agent that is not the visitor's, and 503 when the
 * control plane is not on the tailnet and so receives no run reports.
 */
export type AgentRuns = { kind: "runs"; runs: AgentRun[] } | { kind: "missing" } | { kind: "unavailable" };

export async function loadAgentRuns(gateway: Gateway, cookie: string, agentId: string): Promise<AgentRuns> {
  try {
    const runs = await gateway.get<AgentRun[]>(`/api/agents/${encodeURIComponent(agentId)}/runs`, { cookie });
    return { kind: "runs", runs };
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) {
      return { kind: "missing" };
    }
    if (error instanceof ApiError && error.status === 503) {
      return { kind: "unavailable" };
    }
    throw error;
  }
}
