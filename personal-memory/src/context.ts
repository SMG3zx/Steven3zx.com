export type ContextCandidate = {
  id?: unknown;
  type?: unknown;
  content?: unknown;
  sourceEventIds?: unknown;
  sourceChunkIds?: unknown;
};

/** Put the strongest ranked evidence at the context edges and deduplicate it. */
export function orderContextCandidates(results: ContextCandidate[]) {
  const unique: ContextCandidate[] = [];
  const seen = new Set<string>();
  for (const result of results) {
    const key = `${String(result.type ?? "evidence")}:${String(result.id ?? "unknown")}`;
    if (seen.has(key)) continue;
    seen.add(key);
    unique.push(result);
  }
  const start: ContextCandidate[] = [];
  const end: ContextCandidate[] = [];
  unique.forEach((result, index) => (index % 2 === 0 ? start.push(result) : end.unshift(result)));
  return [...start, ...end];
}

export function buildContextPack(results: ContextCandidate[], maxChars: number) {
  const lines: string[] = [];
  const included: ContextCandidate[] = [];
  let usedChars = 0;
  const ordered = orderContextCandidates(results);
  for (const result of ordered) {
    const provenance = [
      ...(Array.isArray(result.sourceEventIds) ? result.sourceEventIds.map(String) : []),
      ...(Array.isArray(result.sourceChunkIds) ? result.sourceChunkIds.map(String) : []),
    ];
    const sourceSuffix = provenance.length ? ` | sources: ${provenance.join(", ")}` : "";
    const line = `[${String(result.type ?? "evidence")}:${String(result.id ?? "unknown")}] ${String(result.content ?? "")}${sourceSuffix}`;
    const separator = lines.length ? 1 : 0;
    if (usedChars + separator + line.length > maxChars) continue;
    lines.push(line);
    included.push(result);
    usedChars += separator + line.length;
  }
  return {
    context: lines.join("\n"),
    citationLocked: true,
    citationFormat: "Use [type:id] citations and do not assert facts without retrieved evidence.",
    results: included,
    omittedResults: Math.max(0, ordered.length - included.length),
    truncated: included.length < ordered.length,
    characterCount: usedChars,
  };
}
