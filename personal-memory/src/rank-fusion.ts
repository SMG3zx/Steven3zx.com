export type FusionMethod = "lexical" | "vector" | "graph";

export type FusionCandidate = Record<string, unknown> & {
  type: string;
  id: string;
  score?: number;
};

export type FusionWeights = Record<FusionMethod, number>;

/** Keep lexical, vector, and graph scores independent; raw index scores are not calibrated. */
export function reciprocalRankFuse(
  sources: Record<FusionMethod, FusionCandidate[]>,
  weights: FusionWeights,
  k = 60,
): FusionCandidate[] {
  const combined = new Map<string, FusionCandidate>();
  for (const method of ["lexical", "vector", "graph"] as const) {
    const ranked = [...(sources[method] ?? [])].sort((left, right) => Number(right.score ?? 0) - Number(left.score ?? 0));
    for (const [index, item] of ranked.entries()) {
      const key = `${item.type}:${item.id}`;
      const contribution = weights[method] / (k + index + 1);
      const existing = combined.get(key);
      if (!existing) {
        combined.set(key, { ...item, score: contribution, fusionScore: contribution, retrievalMethods: [method], retrievalMethod: method });
        continue;
      }
      const methods = new Set<string>([
        ...(Array.isArray(existing.retrievalMethods) ? existing.retrievalMethods.map(String) : []),
        method,
      ]);
      const fusionScore = Number(existing.fusionScore ?? existing.score ?? 0) + contribution;
      combined.set(key, {
        ...existing,
        retrievalMethods: [...methods],
        fusionScore,
        score: fusionScore,
        retrievalMethod: methods.size > 1 ? "hybrid" : method,
      });
    }
  }
  return [...combined.values()].sort((left, right) => Number(right.score ?? 0) - Number(left.score ?? 0));
}
