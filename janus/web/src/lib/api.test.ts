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
