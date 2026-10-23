import { describe, expect, test } from "bun:test";
import { commitLine, importLabel, shortSha, sourceLabel, type ImportJob, type ProjectSummary } from "./projects";

const base: ProjectSummary = { id: "p1", name: "hello-wasm", description: "" };

describe("sourceLabel", () => {
  test("names the GitHub repository a project was imported from", () => {
    expect(sourceLabel({ ...base, repository: "example/hello-wasm" })).toBe("example/hello-wasm");
  });

  test("calls a project with no repository an empty project", () => {
    expect(sourceLabel(base)).toBe("Empty project");
  });
});

describe("shortSha", () => {
  test("keeps the first seven characters of a commit", () => {
    expect(shortSha("0123456789abcdef")).toBe("0123456");
  });
});

describe("commitLine", () => {
  test("shows the short commit and the first line of its message", () => {
    expect(commitLine({ sha: "0123456789abcdef", message: "Fix the parser\n\nDetails", date: "2026-10-20T10:00:00Z" })).toBe(
      "0123456 Fix the parser",
    );
  });
});

describe("importLabel", () => {
  const job: ImportJob = {
    id: "j1",
    status: "running",
    progress: 42,
    project_id: "p1",
    repository: "example/hello-wasm",
    branch: "main",
    commit: "0123456789abcdef",
  };

  test("reports a running import with its progress", () => {
    expect(importLabel(job)).toEqual({ label: "Importing, 42% done", done: false, failed: false });
  });

  test("reports a finished import as done", () => {
    expect(importLabel({ ...job, status: "complete", progress: 100 })).toEqual({
      label: "Imported",
      done: true,
      failed: false,
    });
  });

  test("reports a failed import with the reason", () => {
    expect(importLabel({ ...job, status: "failed", error: "repository not found" })).toEqual({
      label: "Failed: repository not found",
      done: true,
      failed: true,
    });
  });
});
