export type CitationResult = { id?: unknown; type?: unknown; evidence?: Array<{ id?: unknown; content?: unknown }> };

export type CitationLockedAnswerValidation = {
  valid: boolean;
  reason?: string;
  citations: string[];
  invalid?: string[];
  uncitedSegments?: string[];
};

function answerSegments(answer: string) {
  return answer
    .split(/\r?\n+/)
    .flatMap((line) => line.match(/[^.!?]+(?:[.!?]+\s*(?:\[[a-z_]+:[^\]]+\]\s*)*)?/gi) ?? [])
    .map((segment) => segment.trim())
    .filter(Boolean);
}

/** Require every cited memory identifier to come from the retrieved evidence set. */
export function validateCitationLockedAnswer(answer: string, results: CitationResult[]): CitationLockedAnswerValidation {
  const available = new Set<string>();
  for (const result of results) {
    if (result.id !== undefined) available.add(`${String(result.type ?? "evidence")}:${String(result.id)}`);
    for (const evidence of result.evidence ?? []) {
      if (evidence.id !== undefined) available.add(`${String(result.type ?? "evidence")}:${String(evidence.id)}`);
    }
  }
  const citations = [...answer.matchAll(/\[([a-z_]+):([^\]]+)\]/gi)].map((match) => `${match[1]}:${match[2]}`);
  if (!citations.length) return { valid: false, reason: "Answer must cite retrieved evidence using [type:id].", citations: [] };
  const invalid = citations.filter((citation) => !available.has(citation));
  if (invalid.length) return { valid: false, reason: "Answer contains a citation that was not retrieved.", citations, invalid };
  const uncitedSegments = answerSegments(answer).filter((segment) => !/\[[a-z_]+:[^\]]+\]/i.test(segment));
  return uncitedSegments.length
    ? { valid: false, reason: "Every factual answer segment must cite retrieved evidence.", citations, uncitedSegments }
    : { valid: true, citations };
}
