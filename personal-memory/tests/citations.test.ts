import { expect, test } from "bun:test";
import { validateCitationLockedAnswer } from "../src/citations";

test("citation-locked answers require citations from retrieved evidence", () => {
  const results = [{ type: "memory", id: "m1", content: "Neo4j is used." }];
  expect(validateCitationLockedAnswer("Neo4j is used. [memory:m1]", results).valid).toBe(true);
  expect(validateCitationLockedAnswer("Neo4j is used.", results).valid).toBe(false);
  expect(validateCitationLockedAnswer("Neo4j is used. [memory:unknown]", results).valid).toBe(false);
});

test("citation locking rejects an answer with an unsupported second claim", () => {
  const results = [{ type: "memory", id: "m1", content: "Neo4j is used." }];
  const validation = validateCitationLockedAnswer("Neo4j is used. [memory:m1] The system is production-ready.", results);
  expect(validation.valid).toBe(false);
  expect(validation.uncitedSegments?.length).toBe(1);
});
