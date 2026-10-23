import { describe, expect, test } from "bun:test";
import { generate } from "../src/codegen/generate";
import { LESSONS } from "../src/shared/lessons";
import { runProgram } from "../src/server/go";

describe("practice rooms", () => {
  for (const lesson of LESSONS) {
    test(`${lesson.title}: the solution generates without errors`, () => {
      const { diagnostics } = generate(lesson.solution);
      expect(diagnostics.filter((d) => d.level === "error")).toEqual([]);
    });

    test(`${lesson.title}: the starter is a valid program to finish`, () => {
      const { diagnostics } = generate(lesson.starter);
      expect(diagnostics.filter((d) => d.level === "error")).toEqual([]);
    });

    test(`${lesson.title}: the solution prints the expected output`, async () => {
      if (!Bun.which("go")) return;
      const { source } = generate(lesson.solution);
      const run = await runProgram(lesson.id, source);
      expect(run.ok).toBe(true);
      expect(run.output.trim()).toBe(lesson.expected);
    });
  }
});
