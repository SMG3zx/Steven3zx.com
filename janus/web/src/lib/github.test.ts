import { describe, expect, test } from "bun:test";
import { chooseBranch, loadRepositoryChoices } from "./github";

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

describe("loadRepositoryChoices", () => {
  // The gateway answers with a bare list for branches and commits, and a wrapper for repositories.
  function fakeGateway(paths: string[]) {
    return {
      get: async (path: string) => {
        paths.push(path);
        if (path === "/api/github/repos") {
          return { connected: true, repositories: [{ full_name: "SMG3zx/demo", private: false }] };
        }
        if (path.startsWith("/api/github/branches")) {
          return [{ name: "main", commit: { sha: "a".repeat(40) } }, { name: "dev", commit: { sha: "b".repeat(40) } }];
        }
        return [{ sha: "a".repeat(40), message: "Add entrypoint", date: "2026-10-20T10:00:00Z" }];
      },
    } as unknown as Parameters<typeof loadRepositoryChoices>[0];
  }

  test("lists the branches of the chosen repository and the commits of the chosen branch", async () => {
    const paths: string[] = [];
    const choices = await loadRepositoryChoices(fakeGateway(paths), "janus_session=abc", "SMG3zx/demo", "dev");

    expect(choices.branches).toEqual(["main", "dev"]);
    expect(choices.branch).toBe("dev");
    expect(choices.commits.map((commit) => commit.sha)).toEqual(["a".repeat(40)]);
    expect(paths).toContain("/api/github/commits?repository=SMG3zx%2Fdemo&branch=dev");
  });

  test("asks for no branches or commits until a repository is chosen", async () => {
    const paths: string[] = [];
    const choices = await loadRepositoryChoices(fakeGateway(paths), "", "", "");

    expect(choices.repositories).toHaveLength(1);
    expect(choices.commits).toEqual([]);
    expect(paths).toEqual(["/api/github/repos"]);
  });
});
