import { describe, expect, test } from "bun:test";
import { readConfig } from "./config";

describe("readConfig", () => {
  test("reads the gateway address and the public address", () => {
    const config = readConfig({
      JANUS_API_URL: "http://gateway:8080",
      JANUS_WEB_URL: "https://janus.steven3zx.com",
    });
    expect(config.apiUrl).toBe("http://gateway:8080");
    expect(config.webUrl).toBe("https://janus.steven3zx.com");
  });

  test("strips a trailing slash from both addresses", () => {
    const config = readConfig({
      JANUS_API_URL: "http://gateway:8080/",
      JANUS_WEB_URL: "https://janus.steven3zx.com/",
    });
    expect(config.apiUrl).toBe("http://gateway:8080");
    expect(config.webUrl).toBe("https://janus.steven3zx.com");
  });

  test("fails when the gateway address is missing", () => {
    expect(() => readConfig({ JANUS_WEB_URL: "https://janus.steven3zx.com" })).toThrow(
      "JANUS_API_URL",
    );
  });
});
