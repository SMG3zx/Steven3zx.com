export type LongMemEvalTurn = { role: "user" | "assistant" | string; content: string; has_answer?: boolean };
export type LongMemEvalInstance = {
  question_id: string;
  question_type: string;
  question: string;
  answer: string;
  question_date?: string;
  haystack_session_ids: string[];
  haystack_dates: string[];
  haystack_sessions: LongMemEvalTurn[][];
  answer_session_ids?: string[];
};

export type LongMemEvalEvent = {
  id: string;
  kind: "user_message" | "agent_message";
  content: string;
  source: "longmemeval";
  projectId: string;
  sessionId: string;
  userId: string;
  occurredAt?: string;
  metadata: { datasetVersion: string; questionId: string; turn: number; hasAnswer: boolean };
};

export type LongMemEvalCase = LongMemEvalInstance & { datasetVersion: string; projectId: string; userId: string; events: LongMemEvalEvent[] };

function stableId(value: string) {
  let hash = 2166136261;
  for (const character of value) hash = Math.imul(hash ^ character.charCodeAt(0), 16777619);
  return (hash >>> 0).toString(16).padStart(8, "0");
}

function normalizeDate(value: string | undefined, fallback: string) {
  if (!value) return fallback;
  const timestamp = Date.parse(value.replace(/\//g, "-"));
  return Number.isFinite(timestamp) ? new Date(timestamp).toISOString() : fallback;
}

export function parseLongMemEval(text: string, limit = Number.POSITIVE_INFINITY): LongMemEvalCase[] {
  const value = JSON.parse(text) as unknown;
  if (!Array.isArray(value)) throw new Error("LongMemEval file must contain a JSON array");
  return value.slice(0, limit).map((raw, index) => {
    const item = raw as Partial<LongMemEvalInstance>;
    if (!item.question_id || !item.question || !Array.isArray(item.haystack_sessions) || !Array.isArray(item.haystack_session_ids) || !Array.isArray(item.haystack_dates)) throw new Error(`invalid LongMemEval item at index ${index}`);
    if (item.haystack_sessions.length !== item.haystack_session_ids.length || item.haystack_sessions.length !== item.haystack_dates.length) throw new Error(`LongMemEval session arrays differ in length for ${item.question_id}`);
    const datasetVersion = `longmemeval:${stableId(`${item.question_id}:${item.question_type ?? "unknown"}`)}`;
    const userId = `longmemeval-user-${stableId(String(item.question_id))}`;
    const projectId = `longmemeval-project-${stableId(String(item.question_id))}`;
    const events: LongMemEvalEvent[] = [];
    item.haystack_sessions.forEach((session, sessionIndex) => {
      if (!Array.isArray(session)) throw new Error(`invalid session ${sessionIndex} for ${item.question_id}`);
      session.forEach((turn, turnIndex) => {
        if (!turn?.content?.trim()) return;
        events.push({ id: `lme-${stableId(`${item.question_id}:${sessionIndex}:${turnIndex}`)}`, kind: turn.role === "user" ? "user_message" : "agent_message", content: turn.content, source: "longmemeval", projectId, sessionId: item.haystack_session_ids![sessionIndex], userId, occurredAt: normalizeDate(item.haystack_dates![sessionIndex], `2020-01-01T00:00:${String(sessionIndex).padStart(2, "0")}Z`), metadata: { datasetVersion, questionId: String(item.question_id), turn: turnIndex, hasAnswer: Boolean(turn.has_answer) } });
      });
    });
    return { ...(item as LongMemEvalInstance), question_type: item.question_type ?? "unknown", answer: item.answer ?? "", answer_session_ids: item.answer_session_ids ?? [], datasetVersion, userId, projectId, events };
  });
}

export function sessionRecall(instance: LongMemEvalCase, resultIds: string[]) {
  const eventToSession = new Map(instance.events.map((event) => [event.id, event.sessionId]));
  const retrievedSessions = new Set(resultIds.map((id) => eventToSession.get(id)).filter((id): id is string => Boolean(id)));
  const gold = new Set(instance.answer_session_ids ?? []);
  const overlap = [...gold].filter((id) => retrievedSessions.has(id));
  return { retrievedSessions: [...retrievedSessions], goldSessions: [...gold], recallAny: gold.size === 0 ? true : overlap.length > 0, recallAll: gold.size === 0 ? true : overlap.length === gold.size, overlapCount: overlap.length };
}

export function answerTermCoverage(answer: string, expected: string) {
  const terms = expected.toLowerCase().match(/[a-z0-9][a-z0-9_-]{2,}/g) ?? [];
  const text = answer.toLowerCase();
  const unique = [...new Set(terms)];
  return unique.length ? unique.filter((term) => text.includes(term)).length / unique.length : 1;
}
