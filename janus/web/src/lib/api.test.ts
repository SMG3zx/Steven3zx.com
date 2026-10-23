import { describe, expect, test } from "bun:test";
import { ApiError, createApi } from "./api";

function recordingFetch(response: Response) {
  const calls: { url: string; init: RequestInit }[] = [];
  const fetchImpl = (async (url: string, init: RequestInit) => {
    calls.push({ url, init });
    return response;
  }) as unknown as typeof fetch;
  return { fetchImpl, calls };
}

describe("createApi", () => {
  test("sends the visitor's cookie to the gateway and returns the JSON body", async () => {
    const { fetchImpl, calls } = recordingFetch(
      new Response(JSON.stringify({ id: "me-1" }), { status: 200 }),
    );
    const api = createApi({ baseUrl: "http://gateway:8080", fetch: fetchImpl });

    const me = await api.get<{ id: string }>("/api/me", { cookie: "janus_session=abc" });

    expect(me.id).toBe("me-1");
    expect(calls[0].url).toBe("http://gateway:8080/api/me");
    expect(new Headers(calls[0].init.headers).get("cookie")).toBe("janus_session=abc");
  });

  test("throws an ApiError carrying the status when the gateway refuses", async () => {
    const { fetchImpl } = recordingFetch(new Response("", { status: 401 }));
    const api = createApi({ baseUrl: "http://gateway:8080", fetch: fetchImpl });

    const failure = await api.get("/api/projects", {}).catch((error: unknown) => error);

    expect(failure).toBeInstanceOf(ApiError);
    expect((failure as ApiError).status).toBe(401);
  });

  test("sends no cookie header when the visitor has none", async () => {
    const { fetchImpl, calls } = recordingFetch(new Response("{}", { status: 200 }));
    const api = createApi({ baseUrl: "http://gateway:8080", fetch: fetchImpl });

    await api.get("/api/me", {});

    expect(new Headers(calls[0].init.headers).has("cookie")).toBe(false);
  });
});

describe("createApi.send", () => {
  test("posts a JSON body with the visitor's cookie and returns the raw response", async () => {
    const { fetchImpl, calls } = recordingFetch(
      new Response(JSON.stringify({ id: "u1" }), {
        status: 201,
        headers: { "set-cookie": "janus_session=new; HttpOnly" },
      }),
    );
    const api = createApi({ baseUrl: "http://gateway:9090", fetch: fetchImpl });

    const response = await api.send("POST", "/auth/register", {
      cookie: "janus_session=old",
      body: { email: "a@example.com", password: "correct horse battery" },
    });

    expect(response.status).toBe(201);
    expect(response.headers.get("set-cookie")).toBe("janus_session=new; HttpOnly");
    expect(calls[0].init.method).toBe("POST");
    expect(new Headers(calls[0].init.headers).get("content-type")).toBe("application/json");
    expect(new Headers(calls[0].init.headers).get("cookie")).toBe("janus_session=old");
    expect(JSON.parse(String(calls[0].init.body))).toEqual({
      email: "a@example.com",
      password: "correct horse battery",
    });
  });

  test("does not throw on an error status, so the caller can show the gateway's message", async () => {
    const { fetchImpl } = recordingFetch(new Response('{"error":"invalid email or password"}', { status: 401 }));
    const api = createApi({ baseUrl: "http://gateway:9090", fetch: fetchImpl });

    const response = await api.send("POST", "/auth/login", { body: { email: "a@b.co", password: "x" } });

    expect(response.status).toBe(401);
  });

  test("can hand a redirect back to the caller instead of following it", async () => {
    const { fetchImpl, calls } = recordingFetch(new Response(null, { status: 303, headers: { location: "/projects" } }));
    const api = createApi({ baseUrl: "http://gateway:9090", fetch: fetchImpl });

    const response = await api.send("POST", "/auth/github/disconnect", { cookie: "janus_session=abc", redirect: "manual" });

    expect(response.status).toBe(303);
    expect(calls[0].init.redirect).toBe("manual");
  });
});
