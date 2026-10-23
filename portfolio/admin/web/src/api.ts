import type { Job, JobInput, ScrapedJob } from "./types";

export class Unauthorized extends Error {}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/admin/api${path}`, {
    credentials: "same-origin",
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (res.status === 401) throw new Unauthorized();
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error ?? `${res.status} ${res.statusText}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

export const api = {
  me: () => request<{ username: string }>("/me"),
  agent: {
    restart: () => request<{ status: string }>("/agent/restart", { method: "POST", body: "{}" }),
  },
  login: (username: string, password: string) =>
    request<{ username: string }>("/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request<void>("/logout", { method: "POST", body: "{}" }),
  jobs: {
    list: () => request<{ jobs: Job[] }>("/jobs").then((r) => r.jobs),
    create: (job: JobInput) => request<Job>("/jobs", { method: "POST", body: JSON.stringify(job) }),
    update: (id: string, job: JobInput) =>
      request<Job>(`/jobs/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(job) }),
    remove: (id: string) => request<void>(`/jobs/${encodeURIComponent(id)}`, { method: "DELETE", body: "{}" }),
    scrape: (url: string) =>
      request<ScrapedJob>("/jobs/scrape", { method: "POST", body: JSON.stringify({ url }) }),
  },
};
