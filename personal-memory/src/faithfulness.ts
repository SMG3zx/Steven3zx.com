export type FaithfulnessJudgeInput = {
  query: string;
  answer: string;
  evidence: string[];
};

export type FaithfulnessJudgeResult = {
  faithful: boolean;
  score?: number;
  rationale?: string;
  judgedBy: "external" | "lexical-fallback";
};

/** Optional independent entailment judge exposing POST /judge. */
export class FaithfulnessJudge {
  readonly enabled: boolean;
  private readonly baseUrl?: string;

  constructor(baseUrl = process.env.FAITHFULNESS_JUDGE_BASE_URL) {
    this.baseUrl = baseUrl?.replace(/\/$/, "") || undefined;
    this.enabled = Boolean(this.baseUrl);
  }

  async judge(input: FaithfulnessJudgeInput): Promise<FaithfulnessJudgeResult | null> {
    if (!this.baseUrl) return null;
    const response = await fetch(`${this.baseUrl}/judge`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(input),
    });
    if (!response.ok) throw new Error(`faithfulness judge returned ${response.status}`);
    const payload = (await response.json()) as { faithful?: unknown; verdict?: unknown; score?: unknown; rationale?: unknown };
    const verdict = payload.faithful ?? (payload.verdict === "supported" ? true : payload.verdict === "unsupported" ? false : undefined);
    if (typeof verdict !== "boolean") throw new Error("faithfulness judge returned an invalid verdict");
    return {
      faithful: verdict,
      score: typeof payload.score === "number" ? payload.score : undefined,
      rationale: typeof payload.rationale === "string" ? payload.rationale : undefined,
      judgedBy: "external",
    };
  }
}

export function lexicalFaithfulness(answer: string, evidence: string[], expectedTerms: string[]) {
  const evidenceText = evidence.join(" ").toLowerCase();
  return answer.trim().length > 0 && expectedTerms.every((term) => evidenceText.includes(term.toLowerCase()));
}
