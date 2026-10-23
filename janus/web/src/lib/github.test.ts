import { describe, expect, test } from "bun:test";
import { chooseBranch } from "./github";

describe("chooseBranch", () => {
  test("keeps the branch the visitor asked for when it exists", () => {
    expect(chooseBranch(["main", "dev"], "dev")).toBe("dev");
  });

  test("falls back to the first branch when none was asked for", () => {
    expect(chooseBranch(["main", "dev"], "")).toBe("main");
  });

  test("falls back to the first branch when the asked-for one does not exist", () => {
    expect(chooseBranch(["main"], "gone")).toBe("main");
  });

  test("is empty when the repository has no branches", () => {
    expect(chooseBranch([], "main")).toBe("");
  });
});
