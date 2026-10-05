import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: true,
  timeout: 20_000,
  retries: process.env.CI ? 2 : 0,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: "http://127.0.0.1:5176",
    channel: "chrome",
    headless: true,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "off",
    ...devices["Desktop Chrome"],
  },
  webServer: {
    command: "bun run dev",
    url: "http://127.0.0.1:5176/",
    reuseExistingServer: false,
    timeout: 30_000,
    env: { UI_PORT: "5176", MASTRA_PORT: "4123", FACTORY_TEST_MODE: "1" },
  },
});
