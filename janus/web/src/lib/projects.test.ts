import { describe, expect, test } from "bun:test";
import { sourceLabel, type ProjectSummary } from "./projects";

const base: ProjectSummary = { id: "p1", name: "hello-wasm", description: "" };

describe("sourceLabel", () => {
  test("names the GitHub repository a project was imported from", () => {
    expect(sourceLabel({ ...base, repository: "example/hello-wasm" })).toBe("example/hello-wasm");
  });

  test("calls a project with no repository an empty project", () => {
    expect(sourceLabel(base)).toBe("Empty project");
  });
});
