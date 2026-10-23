import { describe, expect, test } from "bun:test";
import { ApiError } from "./api";
import { formatBytes, formatDuration, formatStarted, loadAgentRuns, runStatus, type AgentRun } from "./runs";

const run: AgentRun = {
  run_id: "r1",
  deployment_id: "d1",
  status: "ok",
  probe: false,
  started_at: "2026-10-23T10:00:00Z",
  fuel: 1200,
  peak_memory_bytes: 2 * 1024 * 1024,
  duration_ms: 850,
  cpu_time_us: 700000,
  network_bytes_in: 512,
  network_bytes_out: 2048,
};

describe("runStatus", () => {
  test("reports a run that finished as ok", () => {
    expect(runStatus(run)).toEqual({ label: "OK", tone: "ok" });
  });

  test("reports a failed run with its reason", () => {
    expect(runStatus({ ...run, status: "failed", error: "trap: unreachable" })).toEqual({
      label: "Failed: trap: unreachable",
      tone: "bad",
    });
  });

  test("reports a failed run without a reason", () => {
    expect(runStatus({ ...run, status: "failed" })).toEqual({ label: "Failed", tone: "bad" });
  });

  test("reports a run that spent all its fuel", () => {
    expect(runStatus({ ...run, status: "out_of_fuel" })).toEqual({ label: "Out of fuel", tone: "bad" });
  });

  test("reports a run stopped by its time limit", () => {
    expect(runStatus({ ...run, status: "timeout" })).toEqual({ label: "Timed out", tone: "bad" });
  });

  test("shows a status it does not know as it came", () => {
    expect(runStatus({ ...run, status: "paused" })).toEqual({ label: "paused", tone: "muted" });
  });
});

describe("formatStarted", () => {
  test("shows the start time in UTC without the T and the fraction", () => {
    expect(formatStarted("2026-10-23T10:00:05.123456Z")).toBe("2026-10-23 10:00:05 UTC");
  });

  test("shows a time it cannot read as it came", () => {
    expect(formatStarted("yesterday")).toBe("yesterday");
  });
});

describe("formatDuration", () => {
  test("shows short runs in milliseconds", () => {
    expect(formatDuration(0)).toBe("0 ms");
    expect(formatDuration(850)).toBe("850 ms");
  });

  test("shows runs of a second or more in seconds", () => {
    expect(formatDuration(1000)).toBe("1.0 s");
    expect(formatDuration(2450)).toBe("2.5 s");
  });

  test("shows runs of a minute or more in minutes and seconds", () => {
    expect(formatDuration(65000)).toBe("1 min 5 s");
    expect(formatDuration(120000)).toBe("2 min 0 s");
  });
});

describe("formatBytes", () => {
  test("shows small sizes in bytes", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
  });

  test("steps up through KiB, MiB and GiB", () => {
    expect(formatBytes(2048)).toBe("2.0 KiB");
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(2 * 1024 * 1024)).toBe("2.0 MiB");
    expect(formatBytes(3 * 1024 * 1024 * 1024)).toBe("3.0 GiB");
  });
});

describe("loadAgentRuns", () => {
  function gatewayAnswering(outcome: AgentRun[] | ApiError) {
    const paths: string[] = [];
    const gateway = {
      get: async (path: string) => {
        paths.push(path);
        if (outcome instanceof ApiError) {
          throw outcome;
        }
        return outcome;
      },
    } as unknown as Parameters<typeof loadAgentRuns>[0];
    return { gateway, paths };
  }

  test("returns the runs the Agent reported, newest first as the gateway sends them", async () => {
    const { gateway, paths } = gatewayAnswering([run]);
    const result = await loadAgentRuns(gateway, "janus_session=abc", "a/1");

    expect(result).toEqual({ kind: "runs", runs: [run] });
    expect(paths).toEqual(["/api/agents/a%2F1/runs"]);
  });

  test("says the Agent is missing when the gateway cannot find it", async () => {
    const { gateway } = gatewayAnswering(new ApiError(404, "/api/agents/a1/runs"));
    expect(await loadAgentRuns(gateway, "", "a1")).toEqual({ kind: "missing" });
  });

  test("says run history is unavailable when the control plane is not on the tailnet", async () => {
    const { gateway } = gatewayAnswering(new ApiError(503, "/api/agents/a1/runs"));
    expect(await loadAgentRuns(gateway, "", "a1")).toEqual({ kind: "unavailable" });
  });

  test("lets any other failure through", async () => {
    const { gateway } = gatewayAnswering(new ApiError(500, "/api/agents/a1/runs"));
    await expect(loadAgentRuns(gateway, "", "a1")).rejects.toThrow("gateway answered 500");
  });
});
