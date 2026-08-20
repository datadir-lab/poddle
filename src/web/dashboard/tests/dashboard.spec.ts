import { test, expect, type Page } from "@playwright/test";

const now = () => new Date().toISOString();
const SEED = [
  { seq: 5, time: now(), pod: "agent1", kind: "request", upstream: "api.anthropic.com", decision: "allow" },
  { seq: 4, time: now(), pod: "agent1", kind: "request", upstream: "api.anthropic.com", decision: "redact", detail: "redacted 1 secret(s)" },
  { seq: 3, time: now(), pod: "agent1", kind: "request", upstream: "metadata.google.internal", decision: "deny", detail: "denied upstream" },
  { seq: 2, time: now(), pod: "agent2", kind: "block", upstream: "evil.example", decision: "block", detail: "egress blocked" },
  { seq: 1, time: now(), pod: "agent2", kind: "block", upstream: "evil.example", decision: "block", detail: "egress blocked" },
];

const PODS = [
  { name: "agent1", state: "running", size: "weak", mode: "headless", policy: "prod", autoscale: true, cpu: "12.5%", memPerc: "68%", mem: "2.7GB / 4GB" },
  { name: "agent2", state: "paused", size: "strong", mode: "interactive", policy: "", autoscale: false, cpu: "0.0%", memPerc: "3%", mem: "120MB / 8GB" },
];

async function mockAudit(page: Page) {
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: true, brokenAt: 0 } }));
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 204, body: "" }));
  await page.route(/\/v1\/audit(\?|$)/, (r) => r.fulfill({ json: SEED }));
}

async function mockPods(page: Page) {
  await page.route(/\/v1\/pods(\?|$)/, (r) => r.fulfill({ json: PODS }));
}

test("loads the console with overview, pods, audit, and policies tabs", async ({ page }) => {
  await page.goto("/");
  // The brand mark is logo-only now; its accessible name still says "poddle".
  await expect(page.getByRole("link", { name: "poddle" })).toBeVisible();
  for (const t of ["Overview", "Pods", "Audit", "Policies"]) {
    await expect(page.getByRole("link", { name: t })).toBeVisible();
  }
});

test("routes with real URLs: deep-link, nav updates the URL, back works", async ({ page }) => {
  await mockPods(page);
  // Deep-link straight to /pods (SPA fallback served the shell) → Pods is active.
  await page.goto("/pods");
  await expect(page.getByRole("link", { name: "Pods" })).toHaveClass(/on/);
  await expect(page.locator("main table")).toBeVisible();

  // Clicking Audit pushes /audit and marks it active.
  await page.getByRole("link", { name: "Audit" }).click();
  await expect(page).toHaveURL(/\/audit$/);
  await expect(page.getByRole("link", { name: "Audit" })).toHaveClass(/on/);

  // Browser back returns to /pods.
  await page.goBack();
  await expect(page).toHaveURL(/\/pods$/);
  await expect(page.getByRole("link", { name: "Pods" })).toHaveClass(/on/);

  // A pod row drills down to /pods/:name.
  await page.locator("tr", { hasText: "agent1" }).first().click();
  await expect(page).toHaveURL(/\/pods\/agent1$/);
  await expect(page.locator(".detail-title")).toHaveText("agent1");
});

test("pod drill-down shows live facts + the pod's audit trail", async ({ page }) => {
  await mockPods(page);
  await mockAudit(page);
  await page.goto("/pods/agent1"); // deep-link into the drill-down

  await expect(page.locator(".detail-title")).toHaveText("agent1");
  await expect(page.locator(".detail-head .state--running")).toBeVisible();
  await expect(page.locator(".detail-head .tag")).toHaveText("auto"); // agent1 autoscales

  // live facts: bound policy (links to /policies/:name), mode
  await expect(page.locator(".facts")).toContainText("Headless"); // mode display-capitalized
  await expect(page.locator(".facts a.fact-link")).toHaveText("prod");
  await page.locator(".facts a.fact-link").click();
  await expect(page).toHaveURL(/\/policies\/prod$/); // policy fact deep-links

  // back to the pod, its audit trail is scoped to agent1 (agent2's block hidden)
  await page.goBack();
  await expect(page.locator("table")).toContainText("api.anthropic.com");
  await expect(page.locator("table")).not.toContainText("evil.example");
});

test("pods view lists the fleet with state, policy, and performance sparklines", async ({ page }) => {
  await mockPods(page);
  await page.goto("/");
  await page.getByRole("link", { name: "Pods" }).click();
  await expect(page.locator("table")).toContainText("agent1");
  await expect(page.locator("table")).toContainText("agent2");
  // state badges + policy binding + autoscale tag surface
  await expect(page.locator(".state--running")).toBeVisible();
  await expect(page.locator(".state--paused")).toBeVisible();
  await expect(page.locator("table")).toContainText("prod");
  await expect(page.locator(".tag")).toContainText("auto");
  // a second poll (3s) gives the sparkline >=2 points, so it renders as an svg
  // with a line + threshold-colored end dot (agent1 CPU 12.5% -> "cool" tone)
  await expect(page.locator("svg.spark").first()).toBeVisible({ timeout: 5000 });
  await expect(page.locator("svg.spark .spark__dot").first()).toBeVisible();
  await expect(page.locator("table")).toContainText("12.5%");
});

test("overview: live pod count, attention on denials, secrets ledger", async ({ page }) => {
  // Audit references 3 distinct pods (incl. a torn-down ghost); the live fleet
  // has only 2. The "pods active" card must reflect the LIVE fleet, not history.
  const audit = [...SEED, { seq: 99, time: now(), pod: "ghost-gone", kind: "request", upstream: "x", decision: "allow" }];
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: true, brokenAt: 0 } }));
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 204, body: "" }));
  await page.route(/\/v1\/audit(\?|$)/, (r) => r.fulfill({ json: audit }));
  await mockPods(page); // 2 live pods: agent1, agent2
  await page.goto("/");

  await expect(page.locator(".cards")).toContainText("pods active");
  await expect(page.locator(".card", { hasText: "pods active" }).locator(".card__num")).toHaveText("2");
  await expect(page.locator(".badge.ok")).toContainText("intact");

  // Denials/blocks surface in Attention.
  await expect(page.getByText("Attention")).toBeVisible();
  await expect(page.locator("main")).toContainText("evil.example");
  await expect(page.locator("main")).toContainText("metadata.google.internal");

  // The secrets ledger shows the redaction; no contradictory "no egress blocked"
  // copy while denials exist.
  await expect(page.locator("main")).not.toContainText("no egress blocked");
  await expect(page.locator(".card", { hasText: "secrets redacted" }).locator(".card__num")).toHaveText("1");
});

test("overview: left sidebar nav, section title, and the charts render", async ({ page }) => {
  await mockPods(page);
  await mockAudit(page);
  await page.goto("/overview");

  // Nav lives in the left rail now, with the active item marked.
  await expect(page.locator(".sidebar .nav__i.on")).toHaveText(/Overview/);
  // The top bar names the current section.
  await expect(page.locator(".topbar__title")).toHaveText("Overview");
  // The three overview charts render: egress columns, decision mix, fleet load.
  await expect(page.locator(".chart .plot")).toBeVisible();
  await expect(page.locator(".posture__bar")).toBeVisible();
  await expect(page.locator(".fleet")).toBeVisible();
  // Global controls moved to the sidebar foot.
  await expect(page.locator(".sidebar__foot .badge")).toBeVisible();
  await expect(page.locator(".sidebar__foot .theme-toggle")).toBeVisible();
});

test("audit feed: text filter + segmented decision filter (no native select)", async ({ page }) => {
  await mockAudit(page);
  await page.goto("/audit"); // deep-link
  await expect(page.locator("select")).toHaveCount(0); // native dropdown replaced
  await expect(page.locator("table")).toContainText("api.anthropic.com");
  await expect(page.locator("table")).toContainText("evil.example");

  // text filter
  await page.getByPlaceholder("filter").fill("evil");
  await expect(page.locator("table")).toContainText("evil.example");
  await expect(page.locator("table")).not.toContainText("api.anthropic.com");
  await page.getByPlaceholder("filter").fill("");

  // segmented decision filter: show only denials
  await page.getByRole("radio", { name: "Deny", exact: true }).click();
  await expect(page.locator("table")).toContainText("metadata.google.internal");
  await expect(page.locator("table")).not.toContainText("evil.example"); // a block, not a deny

  // filtered-empty state is distinguished from no-data-yet (scope to the decision
  // group; a second "All" now exists in the time-range filter).
  await page.getByRole("radiogroup", { name: "filter by decision" }).getByRole("radio", { name: "All", exact: true }).click();
  await page.getByPlaceholder("filter").fill("zzz-no-such-host");
  await expect(page.locator("table")).toContainText("No events match your filter.");
});

test("creates, lists, and deletes a policy through the editor (real /v1/policies)", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("link", { name: "Policies" }).click();
  await page.getByText("new policy").click();
  await expect(page).toHaveURL(/\/policies\/new$/);
  await expect(page.locator("select")).toHaveCount(0); // segmented egress, no native select
  await expect(page.getByText("Allowed destinations")).toBeVisible(); // human label, not allow_upstreams

  await page.locator(".editor input").first().fill("e2e-pol");
  await page.locator(".editor textarea").first().fill("api.anthropic.com\ngit.internal");
  await page.getByRole("radio", { name: "Block", exact: true }).click(); // egress = block
  await page.getByRole("button", { name: "Save" }).click();

  await expect(page.locator(".list")).toContainText("e2e-pol");
  await expect(page).toHaveURL(/\/policies\/e2e-pol$/); // save deep-links to the policy

  await page.locator(".list a", { hasText: "e2e-pol" }).click();
  // the egress mode round-tripped through the file store and shows as active
  await expect(page.getByRole("radio", { name: "Block", exact: true })).toHaveAttribute("aria-checked", "true");
  await page.getByRole("button", { name: "Delete" }).click();
  await expect(page.locator(".list")).not.toContainText("e2e-pol");
});
