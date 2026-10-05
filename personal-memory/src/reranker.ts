export type RerankCandidate = { id: string; content: string; score?: number };

export class RerankerClient {
  private readonly baseUrl = process.env.RERANKER_BASE_URL?.replace(/\/$/, "");

  get enabled() {
    return Boolean(this.baseUrl);
  }

  async rerank(query: string, candidates: RerankCandidate[]) {
    if (!this.baseUrl || !candidates.length) return candidates;
    const response = await fetch(`${this.baseUrl}/rerank`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ query, texts: candidates.map((candidate) => candidate.content), top_n: candidates.length }),
    });
    if (!response.ok) throw new Error(`reranker service returned ${response.status}`);
    const payload = (await response.json()) as Array<{ index: number; score: number }> | { results?: Array<{ index: number; score: number }> };
    const results = Array.isArray(payload) ? payload : payload.results;
    if (!results?.every((item) => Number.isInteger(item.index) && Number.isFinite(item.score))) throw new Error("reranker service returned an invalid response");
    return results.flatMap((item) => {
      const candidate = candidates[item.index];
      return candidate ? [{ ...candidate, score: item.score }] : [];
    });
  }
}
