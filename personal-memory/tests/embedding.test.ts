import { afterEach, expect, test } from "bun:test";
import { EmbeddingClient } from "../src/embeddings";

const originalFetch = globalThis.fetch;
const originalBaseUrl = process.env.EMBEDDING_BASE_URL;

afterEach(() => {
  globalThis.fetch = originalFetch;
  if (originalBaseUrl === undefined) delete process.env.EMBEDDING_BASE_URL;
  else process.env.EMBEDDING_BASE_URL = originalBaseUrl;
});

test("embedding client sends an instruction-wrapped query", async () => {
  process.env.EMBEDDING_BASE_URL = "http://embedding.test";
  let received: unknown;
  globalThis.fetch = (async (_input, init) => {
    received = JSON.parse(String(init?.body));
    return Response.json([[0.1, 0.2, 0.3]]);
  }) as typeof fetch;

  const client = new EmbeddingClient();
  expect(client.enabled).toBe(true);
  expect(await client.embedQuery("what did we decide?")).toEqual([0.1, 0.2, 0.3]);
  expect(received).toEqual({ inputs: ["Instruct: Retrieve memories and source events relevant to the user's query\nQuery: what did we decide?"] });
});

test("embedding client accepts the object response shape", async () => {
  process.env.EMBEDDING_BASE_URL = "http://embedding.test";
  globalThis.fetch = (async () => Response.json({ embeddings: [[1, 2]] })) as unknown as typeof fetch;
  expect(await new EmbeddingClient().embedDocuments(["hello"])).toEqual([[1, 2]]);
});
