import { expect, test } from "bun:test";
import { probeProvider } from "../src/provider-status";

test("provider status distinguishes disabled and ready endpoints", async () => {
  expect(await probeProvider(undefined)).toEqual({ configured: false, state: "disabled" });
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => new Response(null, { status: 200 })) as unknown as typeof fetch;
  try { expect(await probeProvider("http://provider.test")).toEqual({ configured: true, state: "ready", httpStatus: 200 }); }
  finally { globalThis.fetch = originalFetch; }
});

test("provider status times out as unreachable", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => await new Promise<Response>((_, reject) => {
    init?.signal?.addEventListener("abort", () => reject(new Error("aborted")));
  })) as unknown as typeof fetch;
  try { expect((await probeProvider("http://provider.test", 1)).state).toBe("unreachable"); }
  finally { globalThis.fetch = originalFetch; }
});
