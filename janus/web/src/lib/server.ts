import { createApi } from "./api";
import { readConfig } from "./config";

/** The gateway client for server-side rendering, built from the environment. */
export function gatewayFromEnv(env: Record<string, string | undefined> = process.env) {
  const config = readConfig(env);
  return createApi({ baseUrl: config.apiUrl });
}
