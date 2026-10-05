import { expect, test } from "bun:test";
import { FaithfulnessJudge, lexicalFaithfulness } from "../src/faithfulness";

test("lexical faithfulness requires evidence support and a non-empty answer", () => {
  expect(lexicalFaithfulness("answer", ["Qwen3 is used"], ["qwen3"])).toBe(true);
  expect(lexicalFaithfulness("answer", ["unrelated"], ["qwen3"])).toBe(false);
  expect(lexicalFaithfulness("", ["qwen3"], ["qwen3"])).toBe(false);
});

test("faithfulness judge accepts supported verdict responses", async () => {
  process.env.FAITHFULNESS_JUDGE_BASE_URL = "http://judge.test";
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => Response.json({ verdict: "supported", score: 0.91 })) as unknown as typeof fetch;
  try {
    const result = await new FaithfulnessJudge().judge({ query: "q", answer: "a", evidence: ["e"] });
    expect(result?.faithful).toBe(true);
    expect(result?.judgedBy).toBe("external");
  } finally {
    globalThis.fetch = originalFetch;
    delete process.env.FAITHFULNESS_JUDGE_BASE_URL;
  }
});
