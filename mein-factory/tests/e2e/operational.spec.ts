import { expect, test } from "@playwright/test";

test("launches the real local services and starts a factory flow", async ({ page, request }) => {
  const health = await request.get("http://127.0.0.1:4123/health");
  expect(health.ok()).toBe(true);
  expect(await health.json()).toMatchObject({ ok: true, service: "meinfactory-mastra" });

  await page.goto("/");
  await page.getByPlaceholder("What would you like to build?").fill("Build an operational smoke-test project");
  await page.getByRole("button", { name: "Create task" }).click();

  await expect(page.getByText("Factory preview complete — approval required for writes")).toBeVisible();
  await expect(page.getByText("Operational test plan: the local factory flow reached the approval gate.")).toBeVisible();
});
