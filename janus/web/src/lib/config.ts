export type Config = {
  /** The gateway's internal address, for JSON API calls from the server. */
  apiUrl: string;
  /** The public address the site is served from. */
  webUrl: string;
};

function required(env: Record<string, string | undefined>, name: string): string {
  const value = env[name];
  if (!value) {
    throw new Error(`${name} is not set`);
  }
  return value.replace(/\/+$/, "");
}

export function readConfig(env: Record<string, string | undefined>): Config {
  return {
    apiUrl: required(env, "JANUS_API_URL"),
    webUrl: required(env, "JANUS_WEB_URL"),
  };
}
