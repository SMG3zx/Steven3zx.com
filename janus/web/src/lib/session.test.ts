import { describe, expect, test } from "bun:test";
import { ApiError } from "./api";
import { loadSignedInUser, safeTarget, signInPath } from "./session";

const signedIn = { get: async <T>() => ({ id: "me-1", name: "Steven" }) as T };
const signedOut = {
  get: async <T>(): Promise<T> => {
    throw new ApiError(401, "/api/me");
  },
};

describe("loadSignedInUser", () => {
  test("returns the user when the gateway accepts the cookie", async () => {
    const result = await loadSignedInUser(signedIn, "janus_session=abc", "/projects");
    expect(result).toEqual({ user: { id: "me-1", name: "Steven" } });
  });

  test("sends a signed-out visitor to the homepage, keeping the page they asked for", async () => {
    const result = await loadSignedInUser(signedOut, "", "/projects/new");
    expect(result).toEqual({ redirectTo: "/?next=%2Fprojects%2Fnew" });
  });

  test("rethrows failures other than 401", async () => {
    const broken = {
      get: async <T>(): Promise<T> => {
        throw new ApiError(503, "/api/me");
      },
    };
    await expect(loadSignedInUser(broken, "", "/projects")).rejects.toBeInstanceOf(ApiError);
  });
});

describe("signInPath", () => {
  test("keeps only same-site paths as the return target", () => {
    expect(signInPath("/agents")).toBe("/?next=%2Fagents");
    expect(signInPath("https://evil.example/")).toBe("/");
    expect(signInPath("//evil.example/")).toBe("/");
  });
});

describe("safeTarget", () => {
  test("keeps a same-site path and falls back for anything else", () => {
    expect(safeTarget("/agents", "/projects")).toBe("/agents");
    expect(safeTarget("https://evil.example/", "/projects")).toBe("/projects");
    expect(safeTarget("//evil.example/", "/projects")).toBe("/projects");
    expect(safeTarget("", "/projects")).toBe("/projects");
  });
});
