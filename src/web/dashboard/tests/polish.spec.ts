import { test, expect, type Page } from "@playwright/test";

// e2e for the dashboard polish: theming, loading skeletons, the live-stream
// indicator, the richer audit filters (time range + per-decision counts), CSV
// export, and keyboard-operable rows. These drive the real `poddle dashboard`
// binary (embedded SPA) with the /v1 data mocked at the network layer.

const iso = (secAgo: number) => new Date(Date.now() - secAgo * 1000).toISOString();

// 3 recent events (< 15m) and 2 old (> 15m).
const EVENTS = [
  { seq: 6, time: iso(30), pod: "agent1", kind: "request", upstream: "api.anthropic.com", decision: "allow", detail: "provider key injected" },
  { seq: 5, time: iso(120), pod: "agent1", kind: "request", upstream: "api.github.com", decision: "redact", detail: "redacted 2 secret(s)" },
  { seq: 4, time: iso(300), pod: "agent2", kind: "request", upstream: "metadata.google.internal", decision: "deny", detail: "not in allowlist" },
  { seq: 3, time: iso(2000), pod: "agent2", kind: "request", upstream: "169.254.169.254", decision: "block", detail: "egress blocked" },
  { seq: 2, time: iso(5000), pod: "agent1", kind: "request", upstream: "api.anthropic.com", decision: "allow", detail: "provider key injected" },
];
const PODS = [
  { name: "agent1", state: "running", size: "weak", mode: "headless", policy: "prod", autoscale: true, cpu: "12%", memPerc: "60%", mem: "2.4GB / 4GB" },
  { name: "agent2", state: "paused", size: "strong", mode: "interactive", policy: "", autoscale: false, cpu: "0%", memPerc: "3%", mem: "120MB / 8GB" },
];

async function mockV1(page: Page, opts: { events?: unknown[]; pods?: unknown[]; delayMs?: number } = {}) {
  const events = opts.events ?? EVENTS;
  const pods = opts.pods ?? PODS;
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 204, body: "" }));
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: true, brokenAt: 0 } }));
  const maybeDelay = async () => { if (opts.delayMs) await new Promise((res) => setTimeout(res, opts.delayMs)); };
  await page.route(/\/v1\/audit(\?|$)/, async (r) => { await maybeDelay(); return r.fulfill({ json: events }); });
  await page.route(/\/v1\/pods(\?|$)/, async (r) => { await maybeDelay(); return r.fulfill({ json: pods }); });
  await page.route(/\/v1\/policies(\?|$)/, (r) => r.fulfill({ json: [] }));
}

test.describe("theming", () => {
  test("defaults to the OS colour scheme (dark)", async ({ browser }) => {
    const ctx = await browser.newContext({ colorScheme: "dark" });
    const page = await ctx.newPage();
    await mockV1(page);
    await page.goto("/overview");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
    await ctx.close();
  });

  test("toggle flips the theme and persists across a reload", async ({ page }) => {
    await mockV1(page);
    await page.goto("/overview");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");

    await page.locator(".theme-toggle").click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
    await expect(page.locator(".theme-toggle")).toHaveAttribute("aria-pressed", "true");

    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark"); // localStorage-backed
  });
});

test("shows a loading skeleton before the first fetch resolves", async ({ page }) => {
  await mockV1(page, { events: [], pods: [], delayMs: 1200 });
  await page.goto("/overview");
  // Skeleton is up while the (delayed) data is in flight...
  await expect(page.locator(".skel").first()).toBeVisible();
  // ...then it resolves to the real (empty) state, not a permanent skeleton.
  await expect(page.locator(".panel.empty").first()).toBeVisible({ timeout: 4000 });
  await expect(page.locator(".skel")).toHaveCount(0);
});

test("audit: time-range filter narrows the feed", async ({ page }) => {
  await mockV1(page);
  await page.goto("/audit");
  await expect(page.locator("main")).toContainText("5 events"); // all
  // 15m keeps only the 3 recent events (30s, 120s, 300s ago).
  await page.getByRole("radiogroup", { name: "time range" }).getByRole("radio", { name: "15m", exact: true }).click();
  await expect(page.locator("main")).toContainText("3 events");
  await expect(page.locator("table")).not.toContainText("169.254.169.254"); // 2000s ago, excluded
});

test("audit: per-decision counts show on the decision filter", async ({ page }) => {
  await mockV1(page);
  await page.goto("/audit");
  const group = page.getByRole("radiogroup", { name: "filter by decision" });
  // EVENTS: allow x2, redact x1, deny x1, block x1 -> All 5.
  await expect(group.getByRole("radio", { name: "All", exact: true }).locator(".seg__badge")).toHaveText("5");
  await expect(group.getByRole("radio", { name: "Allow", exact: true }).locator(".seg__badge")).toHaveText("2");
  await expect(group.getByRole("radio", { name: "Deny", exact: true }).locator(".seg__badge")).toHaveText("1");
});

test("audit: Export CSV downloads the filtered trail", async ({ page }) => {
  await mockV1(page);
  await page.goto("/audit");
  // Narrow to denials, then export just those.
  await page.getByRole("radiogroup", { name: "filter by decision" }).getByRole("radio", { name: "Deny", exact: true }).click();

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: "Export CSV" }).click(),
  ]);
  expect(download.suggestedFilename()).toBe("poddle-audit.csv");

  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const c of stream) chunks.push(c as Buffer);
  const csv = Buffer.concat(chunks).toString("utf8");
  expect(csv.split("\n")[0]).toContain("time,pod,kind,decision,upstream");
  expect(csv).toContain("metadata.google.internal"); // the one deny row
  expect(csv).not.toContain("api.anthropic.com"); // an allow row, filtered out
});

test("pods: rows are keyboard-operable (focus + Enter drills down)", async ({ page }) => {
  await mockV1(page);
  await page.goto("/pods");
  const row = page.locator("tr.clickable", { hasText: "agent1" }).first();
  await row.focus();
  await expect(row).toBeFocused();
  await row.press("Enter");
  await expect(page).toHaveURL(/\/pods\/agent1$/);
  await expect(page.locator(".detail-title")).toHaveText("agent1");
});

test("audit: the live-stream indicator is present", async ({ page }) => {
  await mockV1(page);
  await page.goto("/audit");
  // The stream is mocked (no real feed), so it reports a non-live state; the
  // point is the indicator renders and reflects connection status.
  await expect(page.locator(".live")).toBeVisible();
});
