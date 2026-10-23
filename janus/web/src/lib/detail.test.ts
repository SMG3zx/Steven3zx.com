import { describe, expect, test } from "bun:test";
import { buildLabel, deploymentLabel, entrypointCandidates, targetsFor, type Build, type Deployment } from "./detail";

const build: Build = {
  id: "b1",
  status: "running",
  progress: 40,
  target: "wasip1-wasm",
  project_id: "p1",
  snapshot_id: "s1",
};

describe("buildLabel", () => {
  test("reports a running build with its progress", () => {
    expect(buildLabel(build)).toEqual({ label: "Building, 40% done", done: false, failed: false });
  });

  test("reports a finished build as done and downloadable", () => {
    expect(buildLabel({ ...build, status: "complete", progress: 100 })).toEqual({
      label: "Built",
      done: true,
      failed: false,
    });
  });

  test("reports a failed build with the reason", () => {
    expect(buildLabel({ ...build, status: "failed", error: "compile error" })).toEqual({
      label: "Failed: compile error",
      done: true,
      failed: true,
    });
  });
});

const deployment: Deployment = {
  id: "d1",
  project_id: "p1",
  build_id: "b1",
  agent_id: "a1",
  state: "healthy",
  deploy_anyway: false,
};

describe("deploymentLabel", () => {
  test("a healthy deployment can be rolled back or stopped", () => {
    expect(deploymentLabel(deployment)).toEqual({ label: "Live", canStop: true, canRollback: true });
  });

  test("a deployment that is still starting can only be stopped", () => {
    expect(deploymentLabel({ ...deployment, state: "starting" })).toEqual({
      label: "Starting",
      canStop: true,
      canRollback: false,
    });
  });

  test("a stopped deployment offers nothing", () => {
    expect(deploymentLabel({ ...deployment, state: "stopped", reason: "replaced" })).toEqual({
      label: "Stopped (replaced)",
      canStop: false,
      canRollback: false,
    });
  });

  test("a failed deployment says why", () => {
    expect(deploymentLabel({ ...deployment, state: "failed", error: "probe failed" })).toEqual({
      label: "Failed: probe failed",
      canStop: false,
      canRollback: false,
    });
  });
});

describe("targetsFor", () => {
  test("offers the targets a Go project can build for", () => {
    expect(targetsFor("Go")).toEqual(["wasip1-wasm"]);
  });

  test("offers the Rust targets for a Rust project", () => {
    expect(targetsFor("Rust")).toEqual(["wasm32-unknown-unknown", "wasm32-wasip1"]);
  });

  test("offers the WASI target a JavaScript or TypeScript project builds for", () => {
    expect(targetsFor("JavaScript/TypeScript")).toEqual(["wasm32-wasip1"]);
  });

  test("offers every target when the language is unknown", () => {
    expect(targetsFor("")).toEqual(["wasm32-unknown-unknown", "wasm32-wasip1", "wasip1-wasm"]);
  });
});

describe("entrypointCandidates", () => {
  test("lists the paths the scan suggested, in the order it found them", () => {
    const metadata = JSON.stringify({
      scan: { entrypoint_candidates: [{ path: "cmd/server/main.go" }, { path: "cmd/worker/main.go" }] },
    });
    expect(entrypointCandidates(metadata)).toEqual(["cmd/server/main.go", "cmd/worker/main.go"]);
  });

  test("returns none when the metadata is missing or not JSON", () => {
    expect(entrypointCandidates("")).toEqual([]);
    expect(entrypointCandidates("not json")).toEqual([]);
    expect(entrypointCandidates("{}")).toEqual([]);
  });
});
