import { test, expect } from "@playwright/test";

test("loads the governance dashboard with both tabs", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator(".brand__name")).toContainText("poddle");
  await expect(page.getByRole("button", { name: "Audit" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Policies" })).toBeVisible();
});

test("renders the audit feed, verify badge, and filter", async ({ page }) => {
  // Mock the audit backend so the feed is deterministic (no daemon needed).
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: true, brokenAt: 0 } }));
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 204, body: "" }));
  await page.route(/\/v1\/audit(\?|$)/, (r) =>
    r.fulfill({
      json: [
        { seq: 2, time: new Date().toISOString(), pod: "agent1", kind: "request", upstream: "api.anthropic.com", decision: "allow" },
        { seq: 1, time: new Date().toISOString(), pod: "agent1", kind: "block", upstream: "evil.example", decision: "deny", detail: "not allow-listed" },
      ],
    }),
  );

  await page.goto("/");
  await expect(page.locator("table")).toContainText("api.anthropic.com");
  await expect(page.locator("table")).toContainText("evil.example");
  await expect(page.locator(".badge.ok")).toContainText("intact");

  await page.getByPlaceholder("filter").fill("evil");
  await expect(page.locator("table")).toContainText("evil.example");
  await expect(page.locator("table")).not.toContainText("api.anthropic.com");
});

test("creates, lists, and deletes a policy through the editor (real /v1/policies)", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Policies" }).click();
  await page.getByText("new policy").click();

  await page.locator("input").first().fill("e2e-pol"); // name
  await page.locator("textarea").first().fill("api.anthropic.com\ngit.internal"); // allow_upstreams
  await page.getByRole("button", { name: "Save" }).click();

  // It persists (real file-backed store) and appears in the list.
  await expect(page.locator(".list")).toContainText("e2e-pol");

  // Reopen it and delete.
  await page.locator(".list button", { hasText: "e2e-pol" }).click();
  await page.getByRole("button", { name: "Delete" }).click();
  await expect(page.locator(".list")).not.toContainText("e2e-pol");
});
