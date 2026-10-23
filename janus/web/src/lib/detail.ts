/** A WASM Build as the gateway reports it. */
export type Build = {
  id: string;
  status: string;
  progress: number;
  target: string;
  error?: string;
  artifact_name?: string;
  project_id: string;
  snapshot_id: string;
};

/** A Deployment as the gateway reports it. */
export type Deployment = {
  id: string;
  project_id: string;
  build_id: string;
  agent_id: string;
  state: string;
  reason?: string;
  error?: string;
  deploy_anyway: boolean;
};

export function buildLabel(build: Build): { label: string; done: boolean; failed: boolean } {
  if (build.status === "failed") {
    return { label: `Failed: ${build.error ?? "unknown error"}`, done: true, failed: true };
  }
  if (build.status === "complete") {
    return { label: "Built", done: true, failed: false };
  }
  return { label: `Building, ${build.progress}% done`, done: false, failed: false };
}

export function deploymentLabel(deployment: Deployment): { label: string; canStop: boolean; canRollback: boolean } {
  switch (deployment.state) {
    case "healthy":
      return { label: "Live", canStop: true, canRollback: true };
    case "queued":
    case "starting":
      return { label: "Starting", canStop: true, canRollback: false };
    case "stopped":
      return { label: `Stopped${deployment.reason ? ` (${deployment.reason})` : ""}`, canStop: false, canRollback: false };
    case "failed":
      return { label: `Failed: ${deployment.error ?? "unknown error"}`, canStop: false, canRollback: false };
    default:
      return { label: deployment.state, canStop: false, canRollback: false };
  }
}

const allTargets = ["wasm32-unknown-unknown", "wasm32-wasip1", "wasip1-wasm"];

/** The WASM targets a project's language can build for; every target when the language is unknown. */
export function targetsFor(language: string): string[] {
  switch (language) {
    case "Go":
      return ["wasip1-wasm"];
    case "Rust":
      return ["wasm32-unknown-unknown", "wasm32-wasip1"];
    case "JavaScript/TypeScript":
      return ["wasm32-wasip1"];
    default:
      return allTargets;
  }
}

/** The entrypoints the import's scan suggested. Empty when the metadata has none. */
export function entrypointCandidates(metadataJSON: string): string[] {
  try {
    const parsed = JSON.parse(metadataJSON) as { scan?: { entrypoint_candidates?: { path?: unknown }[] } };
    const candidates = parsed.scan?.entrypoint_candidates ?? [];
    return candidates.map((candidate) => candidate.path).filter((path): path is string => typeof path === "string");
  } catch {
    return [];
  }
}
