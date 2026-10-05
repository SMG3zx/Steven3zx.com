import { expect, test } from "bun:test";
import { answerTermCoverage, parseLongMemEval, sessionRecall } from "../src/longmemeval";

const fixture = JSON.stringify([{
  question_id: "fixture-1",
  question_type: "knowledge-update",
  question: "Which provider is current?",
  answer: "Qwen3",
  question_date: "2026/01/03",
  haystack_session_ids: ["s1", "s2"],
  haystack_dates: ["2026/01/01", "2026/01/02"],
  haystack_sessions: [[{ role: "user", content: "We used a hosted API." }], [{ role: "user", content: "We switched to Qwen3.", has_answer: true }]],
  answer_session_ids: ["s2"],
}]);

test("parses LongMemEval sessions into isolated, deterministic events", () => {
  const first = parseLongMemEval(fixture)[0];
  const second = parseLongMemEval(fixture)[0];
  expect(first.datasetVersion).toBe(second.datasetVersion);
  expect(first.events).toEqual(second.events);
  expect(first.events[0].sessionId).toBe("s1");
  expect(first.events[1].metadata.hasAnswer).toBe(true);
});

test("scores gold evidence sessions independently of answer text", () => {
  const item = parseLongMemEval(fixture)[0];
  expect(sessionRecall(item, [item.events[1].id]).recallAll).toBe(true);
  expect(sessionRecall(item, [item.events[0].id]).recallAll).toBe(false);
  expect(answerTermCoverage("The current provider is Qwen3", item.answer)).toBe(1);
});
