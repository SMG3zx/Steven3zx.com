import { afterEach, expect, test } from "bun:test";
import { RerankerClient } from "../src/reranker";

const originalFetch = globalThis.fetch;
const originalBaseUrl = process.env.RERANKER_BASE_URL;

afterEach(() => {
  globalThis.fetch = originalFetch;
  if (originalBaseUrl === undefined) delete process.env.RERANKER_BASE_URL;
  else process.env.RERANKER_BASE_URL = originalBaseUrl;
});

test("reranker reorders candidates using provider scores", async () => {
  process.env.RERANKER_BASE_URL = "http://reranker.test";
  globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
    expect(JSON.parse(String(init?.body))).toEqual({ query: "why", texts: ["first", "second"], top_n: 2 });
    return Response.json([{ index: 1, score: 0.9 }, { index: 0, score: 0.2 }]);
  }) as unknown as typeof fetch;
  const results = await new RerankerClient().rerank("why", [{ id: "a", content: "first" }, { id: "b", content: "second" }]);
  expect(results.map((result) => result.id)).toEqual(["b", "a"]);
});
