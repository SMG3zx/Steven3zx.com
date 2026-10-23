import { ApiError } from "./api";

export type CurrentUser = { id: string; name: string };

type Gateway = {
  get<T>(path: string, options: { cookie?: string }): Promise<T>;
};

/**
 * Only a path on this site may be a redirect target. Anything else (an absolute
 * URL, a protocol-relative "//host", an empty string) gives the fallback.
 */
export function safeTarget(target: string, fallback: string): string {
  const isSitePath = target.startsWith("/") && !target.startsWith("//");
  return isSitePath ? target : fallback;
}

export function signInPath(target: string): string {
  const safe = safeTarget(target, "");
  return safe ? `/sign-in?next=${encodeURIComponent(safe)}` : "/sign-in";
}

export async function loadSignedInUser(
  gateway: Gateway,
  cookie: string,
  target: string,
): Promise<{ user: CurrentUser } | { redirectTo: string }> {
  try {
    const user = await gateway.get<CurrentUser>("/api/me", { cookie });
    return { user };
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      return { redirectTo: signInPath(target) };
    }
    throw error;
  }
}
