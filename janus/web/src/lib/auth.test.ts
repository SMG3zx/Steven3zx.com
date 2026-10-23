import { describe, expect, test } from "bun:test";
import { register, signIn } from "./auth";

function gateway(response: Response) {
  return {
    send: async () => response,
  } as unknown as Parameters<typeof signIn>[0];
}

const credentials = { email: "alice@example.com", password: "correct horse battery" };

describe("signIn", () => {
  test("returns the session cookie the gateway issued", async () => {
    const result = await signIn(
      gateway(new Response("{}", { status: 200, headers: { "set-cookie": "janus_session=abc; HttpOnly" } })),
      credentials,
    );
    expect(result).toEqual({ ok: true, setCookie: "janus_session=abc; HttpOnly" });
  });

  test("shows the gateway's message for wrong credentials", async () => {
    const result = await signIn(
      gateway(new Response('{"error":"invalid email or password"}', { status: 401 })),
      credentials,
    );
    expect(result).toEqual({ ok: false, status: 401, message: "invalid email or password" });
  });

  test("uses a plain message when the gateway fails without a readable error", async () => {
    const result = await signIn(gateway(new Response("", { status: 500 })), credentials);
    expect(result).toEqual({ ok: false, status: 500, message: "Something went wrong. Try again." });
  });

  test("treats a success without a cookie as a failure", async () => {
    const result = await signIn(gateway(new Response("{}", { status: 200 })), credentials);
    expect(result.ok).toBe(false);
  });
});

describe("register", () => {
  test("posts to the registration endpoint", async () => {
    const paths: string[] = [];
    const api = {
      send: async (_method: string, path: string) => {
        paths.push(path);
        return new Response("{}", { status: 201, headers: { "set-cookie": "janus_session=new" } });
      },
    } as unknown as Parameters<typeof register>[0];

    const result = await register(api, credentials);

    expect(paths).toEqual(["/auth/register"]);
    expect(result).toEqual({ ok: true, setCookie: "janus_session=new" });
  });
});
