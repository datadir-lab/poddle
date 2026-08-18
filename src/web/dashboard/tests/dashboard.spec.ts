import { test, expect, type Page } from "@playwright/test";

const now = () => new Date().toISOString();
const SEED = [
  { seq: 5, time: now(), pod: "agent1", kind: "request", upstream: "api.anthropic.com", decision: "allow" },
  { seq: 4, time: now(), pod: "agent1", kind: "request", upstream: "api.anthropic.com", decision: "redact", detail: "redacted 1 secret(s)" },
  { seq: 3, time: now(), pod: "agent1", kind: "request", upstream: "metadata.google.internal", decision: "deny", detail: "denied upstream" },
  { seq: 2, time: now(), pod: "agent2", kind: "block", upstream: "evil.example", decision: "block", detail: "egress blocked" },
  { seq: 1, time: now(), pod: "agent2", kind: "block", upstream: "evil.example", decision: "block", detail: "egress blocked" },
];

async function mockAudit(page: Page) {
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: true, brokenAt: 0 } }));
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 204, body: "" }));
  await page.route(/\/v1\/audit(\?|$)/, (r) => r.fulfill({ json: SEED }));
}

test("loads the console with overview, audit, and policies tabs", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator(".brand__name")).toContainText("poddle");
  for (const t of ["Overview", "Audit", "Policies"]) {
    await expect(page.getByRole("button", { name: t })).toBeVisible();
  }
});

test("overview summarises the fleet and lifts attention + secrets", async ({ page }) => {
  await mockAudit(page);
  await page.goto("/");
  await expect(page.locator(".cards")).toContainText("pods active");
  await expect(page.locator(".cards")).toContainText("secrets redacted");
  await expect(page.locator(".cards")).toContainText("blocked / denied");
  await expect(page.locator(".badge.ok")).toContainText("intact");
  await expect(page.getByText("Attention")).toBeVisible();
  // the deny + blocks surface, and the redaction shows in Egress & secrets
  await expect(page.locator("main")).toContainText("evil.example");
  await expect(page.locator("main")).toContainText("metadata.google.internal");
});

test("renders the audit feed, verify badge, and filter", async ({ page }) => {
  await mockAudit(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Audit" }).click(); // default view is Overview now
  await expect(page.locator("table")).toContainText("api.anthropic.com");
  await expect(page.locator("table")).toContainText("evil.example");

  await page.getByPlaceholder("filter").fill("evil");
  await expect(page.locator("table")).toContainText("evil.example");
  await expect(page.locator("table")).not.toContainText("api.anthropic.com");
});

test("creates, lists, and deletes a policy through the editor (real /v1/policies)", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Policies" }).click();
  await page.getByText("new policy").click();

  await page.locator(".editor input").first().fill("e2e-pol");
  await page.locator(".editor textarea").first().fill("api.anthropic.com\ngit.internal");
  await page.getByRole("button", { name: "Save" }).click();

  await expect(page.locator(".list")).toContainText("e2e-pol");

  await page.locator(".list button", { hasText: "e2e-pol" }).click();
  await page.getByRole("button", { name: "Delete" }).click();
  await expect(page.locator(".list")).not.toContainText("e2e-pol");
});
