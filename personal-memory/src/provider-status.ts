export type ProviderStatus = {
  configured: boolean;
  state: "disabled" | "ready" | "reachable" | "unreachable";
  httpStatus?: number;
};

export async function probeProvider(baseUrl: string | undefined, timeoutMs = 1500): Promise<ProviderStatus> {
  const normalized = baseUrl?.replace(/\/$/, "");
  if (!normalized) return { configured: false, state: "disabled" };
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(`${normalized}/health`, { signal: controller.signal });
    return { configured: true, state: response.ok ? "ready" : "reachable", httpStatus: response.status };
  } catch {
    return { configured: true, state: "unreachable" };
  } finally {
    clearTimeout(timeout);
  }
}
