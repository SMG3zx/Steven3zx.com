import { expect, test } from "@playwright/test";

test.describe("MeinFactory dashboard", () => {
  test("side navigation opens each workspace view", async ({ page }) => {
    const destinations = [
      ["/factory/", "Factory floor"],
      ["/projects/", "Projects"],
      ["/runs/", "Runs"],
      ["/agents/", "Agents"],
      ["/tools/", "Tools & MCP"],
      ["/settings/", "Settings"],
    ] as const;

    await page.goto("/");
    for (const [href, heading] of destinations) {
      await page.locator(`a[href="${href}"]`).click();
      await expect(page.getByRole("heading", { name: heading, exact: true })).toBeVisible();
      await page.goto("/");
    }
  });

  test("shows the local factory control plane", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Build something useful." })).toBeVisible();
    await expect(page.getByText("Ollama connected")).toBeVisible();
    await expect(page.getByText("No external connections detected")).toBeVisible();
  });

  test("queues a build request and renders the agent response", async ({ page }) => {
    await page.route("http://127.0.0.1:4123/api/factory/runs", async (route) => {
      await route.fulfill({ status: 202, contentType: "application/json", body: JSON.stringify({ id: "run-123", status: "Factory flow started" }) });
    });
    await page.route("http://127.0.0.1:4123/api/factory/runs/run-123", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ stage: "approval", status: "Plan ready — awaiting approval", plan: "Plan ready: create the project shell, add tests, then review the diff." }),
      });
    });

    await page.goto("/");
    const prompt = page.getByPlaceholder("What would you like to build?");
    await prompt.fill("Build a local notes app");
    await page.getByRole("button", { name: "Create task" }).click();

    await expect(page.getByText("Build a local notes app")).toBeVisible();
    await expect(page.getByText("Plan ready — awaiting approval")).toBeVisible();
    await expect(page.getByText("Plan ready: create the project shell, add tests, then review the diff.")).toBeVisible();
  });

  test("supports the keyboard submit shortcut", async ({ page }) => {
    await page.route("http://127.0.0.1:4123/api/factory/runs", async (route) => {
      await route.fulfill({ status: 202, contentType: "application/json", body: JSON.stringify({ id: "run-keyboard", status: "Factory flow started" }) });
    });
    await page.route("http://127.0.0.1:4123/api/factory/runs/run-keyboard", async (route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ stage: "approval", status: "Keyboard factory flow accepted.", plan: "Keyboard task accepted." }) });
    });

    await page.goto("/");
    await page.getByPlaceholder("What would you like to build?").pressSequentially("Create a test fixture");
    await page.getByPlaceholder("What would you like to build?").press("Control+Enter");
    await expect(page.getByText("Keyboard factory flow accepted.")).toBeVisible();
  });
});
