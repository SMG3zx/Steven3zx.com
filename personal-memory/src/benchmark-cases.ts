export type BenchmarkCase = {
  id: string;
  category: string;
  query: string;
  expectedContains: string[];
  requiresProvenance?: boolean;
  shouldAbstain: boolean;
  reasoningIntensive?: boolean;
};

type ExternalCase = Partial<BenchmarkCase> & {
  question?: string;
  answer?: string;
  expected?: string[];
  gold?: string[];
};

function normalizeCase(input: ExternalCase, index: number): BenchmarkCase {
  const expectedContains = input.expectedContains ?? input.expected ?? input.gold ?? (input.answer ? input.answer.match(/[a-z0-9][a-z0-9_-]{2,}/gi)?.slice(0, 12) ?? [] : []);
  return {
    id: input.id ?? `external-${index + 1}`,
    category: input.category ?? "external",
    query: input.query ?? input.question ?? "",
    expectedContains: expectedContains.map(String),
    requiresProvenance: input.requiresProvenance ?? true,
    shouldAbstain: input.shouldAbstain ?? false,
    reasoningIntensive: input.reasoningIntensive ?? /bright|reasoning|multi[-_ ]?hop/i.test(input.category ?? ""),
  };
}

export function parseBenchmarkCases(text: string, format?: "json" | "jsonl") {
  const trimmed = text.trim();
  if (!trimmed) return [];
  const isJsonl = format === "jsonl" || (format !== "json" && !trimmed.startsWith("["));
  const values = isJsonl ? trimmed.split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line)) : JSON.parse(trimmed);
  if (!Array.isArray(values)) throw new Error("benchmark case file must contain an array or JSONL objects");
  return values.map((value, index) => normalizeCase(value as ExternalCase, index)).filter((testCase) => testCase.query.length > 0);
}
