// Typed client for the firecracker-manager HTTP API. Paths are relative, so the
// UI works both behind the Vite dev proxy and when served by the manager itself.

export type VMState =
  | "stopped"
  | "starting"
  | "running"
  | "paused"
  | "stopping"
  | "error";

export interface Spec {
  name: string;
  kernelPath: string;
  rootfsPath: string;
  vcpus: number;
  memoryMiB: number;
  bootArgs: string;
}

export interface VM {
  id: string;
  spec: Spec;
  state: VMState;
  pid?: number;
  createdAt: string;
  startedAt?: string;
  lastError?: string;
}

export interface HostInfo {
  kvm: boolean;
  kvmError?: string;
  firecrackerBin: string;
  firecracker?: string;
  firecrackerError?: string;
}

export type VMAction = "start" | "stop" | "pause" | "resume";

export class APIError extends Error {}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api${path}`, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new APIError(data.error ?? `${res.status} ${res.statusText}`);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

export const api = {
  host: () => request<HostInfo>("GET", "/host"),
  listVMs: () => request<VM[]>("GET", "/vms"),
  getVM: (id: string) => request<VM>("GET", `/vms/${id}`),
  createVM: (spec: Spec) => request<VM>("POST", "/vms", spec),
  deleteVM: (id: string) => request<void>("DELETE", `/vms/${id}`),
  action: (id: string, action: VMAction) => request<VM>("POST", `/vms/${id}/${action}`),
  sendInput: (id: string, data: string) =>
    request<void>("POST", `/vms/${id}/console/input`, { data }),
  consoleURL: (id: string) => `/api/vms/${id}/console`,
};

/** Actions that are valid for a VM in the given state. */
export function allowedActions(state: VMState): VMAction[] {
  switch (state) {
    case "stopped":
    case "error":
      return ["start"];
    case "running":
      return ["pause", "stop"];
    case "paused":
      return ["resume", "stop"];
    default:
      return [];
  }
}

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
