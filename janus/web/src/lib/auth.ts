export type Credentials = { email: string; password: string };

export type AuthResult =
  | { ok: true; setCookie: string }
  | { ok: false; status: number; message: string };

type Sender = {
  send(method: string, path: string, options: { body: unknown }): Promise<Response>;
};

const genericFailure = "Something went wrong. Try again.";

/** Signs in through the gateway and returns the session cookie it issued. */
export function signIn(gateway: Sender, credentials: Credentials): Promise<AuthResult> {
  return authenticate(gateway, "/auth/login", credentials);
}

/** Registers through the gateway, which signs the new account in. */
export function register(gateway: Sender, credentials: Credentials): Promise<AuthResult> {
  return authenticate(gateway, "/auth/register", credentials);
}

async function authenticate(gateway: Sender, path: string, credentials: Credentials): Promise<AuthResult> {
  const response = await gateway.send("POST", path, { body: credentials });
  if (!response.ok) {
    return { ok: false, status: response.status, message: await errorMessage(response) };
  }
  const setCookie = response.headers.get("set-cookie");
  if (!setCookie) {
    return { ok: false, status: response.status, message: genericFailure };
  }
  return { ok: true, setCookie };
}

/** The gateway's own message when it gives one; a plain message otherwise, so server internals never reach the page. */
async function errorMessage(response: Response): Promise<string> {
  if (response.status >= 500) {
    return genericFailure;
  }
  try {
    const body = (await response.json()) as { error?: unknown };
    return typeof body.error === "string" ? body.error : genericFailure;
  } catch {
    return genericFailure;
  }
}
