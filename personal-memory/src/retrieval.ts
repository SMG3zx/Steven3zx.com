export type RetrievalRoute = "semantic" | "graph" | "temporal" | "procedural";

export type RetrievalComplexity = "simple" | "temporal" | "procedural" | "multi-hop";

export function classifyQueryComplexity(query: string, route = routeQuery(query)): RetrievalComplexity {
  const normalized = query.toLowerCase();
  if (route === "temporal") return "temporal";
  if (route === "procedural") return "procedural";
  if (route === "graph" || /\b(and|both|between|depend|affect|why|compare|related)\b/.test(normalized)) return "multi-hop";
  return "simple";
}

export function retrievalPlan(query: string, route = routeQuery(query)) {
  const complexity = classifyQueryComplexity(query, route);
  const candidateMultiplier = complexity === "simple" ? 2 : complexity === "multi-hop" ? 6 : 4;
  return {
    complexity,
    candidateMultiplier,
    graphLimit: complexity === "simple" ? 4 : complexity === "multi-hop" ? 20 : 10,
    rerankLimit: complexity === "simple" ? 8 : complexity === "multi-hop" ? 24 : 16,
  } as const;
}

/** MemoryBank-inspired retention signal used only for ranking, never deletion. */
export function memoryRetentionScore(input: {
  occurredAt?: unknown;
  confidence?: unknown;
  category?: unknown;
  reinforcementCount?: unknown;
  now?: Date;
}) {
  const timestamp = input.occurredAt ? Date.parse(String(input.occurredAt)) : NaN;
  const ageDays = Number.isFinite(timestamp)
    ? Math.max(0, ((input.now?.getTime() ?? Date.now()) - timestamp) / 86_400_000)
    : 0;
  const confidence = Math.min(1, Math.max(0, Number(input.confidence ?? 1) || 0));
  const reinforcementCount = Math.max(0, Number(input.reinforcementCount ?? 0) || 0);
  const category = String(input.category ?? "");
  const halfLifeDays = category === "working" ? 3 : category === "episode" ? 14 : category === "procedure" ? 180 : 90;
  const reinforcement = 1 + Math.min(1.5, Math.log1p(reinforcementCount) * 0.35);
  return confidence * (0.5 ** (ageDays / (halfLifeDays * reinforcement)));
}

const stopWords = new Set("a an and are about before can did do does for from how i in is it me of on or the this to used we what when where which who why with".split(" "));

export function routeQuery(query: string): RetrievalRoute {
  const normalized = query.toLowerCase();
  if (/\b(when|current|currently|before|after|last|previous|recent|latest|used to)\b/.test(normalized)) return "temporal";
  if (/\b(how do i|how did we|steps|procedure|runbook|recover|deploy|configure|fix)\b/.test(normalized)) return "procedural";
  if (/\b(who|which|what depends|related|connected|relationship|linked|associated|affect)\b/.test(normalized)) return "graph";
  return "semantic";
}

export function shouldAbstain(results: Array<{ score?: unknown; content?: unknown; retrievalMethod?: unknown }>, query?: string) {
  if (!results.length) return { abstain: true, reason: "No relevant evidence was retrieved." };
  const scored = results.map((result) => Number(result.score ?? 0)).filter(Number.isFinite);
  if (query) {
    const terms = query.toLowerCase().match(/[a-z0-9][a-z0-9_-]{2,}/g) ?? [];
    const meaningful = terms.filter((term) => !stopWords.has(term));
    const hasLexicalEvidence = results.some((result) => {
      if (result.retrievalMethod === "vector" || result.retrievalMethod === "graph") return true;
      const content = String(result.content ?? "").toLowerCase();
      return meaningful.some((term) => content.includes(term));
    });
    const distinctiveTerms = meaningful.filter((term) => /[0-9]|[-_]/.test(term) || term.length >= 14);
    const hasDistinctiveEvidence = !distinctiveTerms.length || distinctiveTerms.every((term) => results.some((result) => {
      const content = String(result.content ?? "").toLowerCase();
      return content.includes(term);
    }));
    if (!hasDistinctiveEvidence) return { abstain: true, reason: "No retrieved evidence contains the query's distinctive identifiers." };
    if (meaningful.length && !hasLexicalEvidence) return { abstain: true, reason: "Retrieved evidence did not contain query-linked terms." };
    // Reciprocal-rank fusion intentionally produces small, method-independent
    // scores. Term-linked lexical/hybrid evidence is stronger than the old
    // calibrated-score threshold in that representation.
    if (scored.length && Math.max(...scored) < 0.18 && !hasLexicalEvidence) return { abstain: true, reason: "Retrieved evidence did not meet the confidence threshold." };
    return { abstain: false as const };
  }
  if (scored.length && Math.max(...scored) < 0.18) return { abstain: true, reason: "Retrieved evidence did not meet the confidence threshold." };
  return { abstain: false as const };
}

export function correctiveQuery(query: string) {
  const terms = query.toLowerCase().match(/[a-z0-9][a-z0-9_-]{2,}/g)?.filter((term) => !stopWords.has(term)) ?? [];
  const unique = [...new Set(terms)].slice(0, 10);
  const fallback = unique.join(" ");
  return fallback && fallback !== query.trim().toLowerCase() ? fallback : null;
}

export function assessEvidence(results: Array<{ score?: unknown; retrievalMethod?: unknown }>) {
  const scored = results.map((result) => Number(result.score ?? 0)).filter(Number.isFinite);
  const maxScore = scored.length ? Math.max(...scored) : 0;
  return {
    resultCount: results.length,
    scoredResultCount: scored.length,
    maxScore,
    needsCorrection: results.length === 0 || maxScore < 0.25,
  };
}
