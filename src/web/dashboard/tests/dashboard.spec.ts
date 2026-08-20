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

  await page.locator("#pol-name").fill("e2e-pol");
  await page.getByRole("button", { name: /Add destination/ }).click(); // block builder, not a textarea
  await page.locator(".rule__host").first().fill("api.anthropic.com");
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

test("policy builder: a per-destination method restriction collapses to a summary and expands to edit", async ({ page }) => {
  await page.route(/\/v1\/policies(\/|\?|$)/, (r) => r.fulfill({ json: [] }));
  await mockAudit(page);
  await mockPods(page);
  await page.goto("/policies/new");

  await page.getByRole("button", { name: /Add destination/ }).click();
  await page.locator(".rule__host").first().fill("api.github.com");
  await page.getByRole("button", { name: /limit methods/ }).click(); // reveal the toggles (no JSON)
  await page.getByRole("button", { name: "GET", exact: true }).click(); // restrict to GET

  // "Done" collapses to a clickable summary; clicking it re-expands with GET still on.
  await page.getByRole("button", { name: "Done", exact: true }).click();
  await expect(page.locator(".rule__msum")).toContainText("GET");
  await expect(page.locator(".mchip")).toHaveCount(0); // collapsed
  await page.locator(".rule__msum").click();
  await expect(page.locator(".mchip", { hasText: "GET" })).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator(".mchip", { hasText: "POST" })).toHaveAttribute("aria-pressed", "false");
});

test("policies: the dry-run applies allow-list, deny-list, per-method, and default-deny rules", async ({ page }) => {
  const t = new Date().toISOString();
  const evs = [
    { seq: 5, time: t, pod: "a", kind: "request", upstream: "api.github.com", method: "POST" },          // allowed host, but POST blocked (GET only)
    { seq: 4, time: t, pod: "a", kind: "request", upstream: "api.github.com", method: "GET" },            // allowed host + allowed method -> passes
    { seq: 3, time: t, pod: "a", kind: "request", upstream: "metadata.google.internal", method: "GET" }, // on the deny-list
    { seq: 2, time: t, pod: "a", kind: "request", upstream: "unlisted.example", method: "GET" },          // not allow-listed (default-deny)
  ];
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: true, brokenAt: 0 } }));
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 204, body: "" }));
  await page.route(/\/v1\/audit(\?|$)/, (r) => r.fulfill({ json: evs }));
  await page.route(/\/v1\/policies(\/|\?|$)/, (r) => r.fulfill({ json: [] }));
  await mockPods(page);
  await page.goto("/policies/new");

  // Build in the visual editor: allow api.github.com restricted to GET; block a host.
  await page.getByRole("button", { name: /Add destination/ }).click();
  await page.locator(".rule__host").first().fill("api.github.com");
  await page.getByRole("button", { name: /limit methods/ }).click();
  await page.getByRole("button", { name: "GET", exact: true }).click();
  await page.getByRole("button", { name: /Add blocked host/ }).click();
  await page.locator("input[aria-label='Blocked host']").fill("metadata.google.internal");

  // Only the GET to api.github.com passes; the other three are denied for distinct reasons.
  await expect(page.locator(".dryrun")).toContainText("3 would be denied");
  const list = page.locator(".dryrun__list");
  await expect(list).toContainText("api.github.com");           // POST -> method not allowed
  await expect(list).toContainText("metadata.google.internal"); // on the deny-list
  await expect(list).toContainText("unlisted.example");         // not allow-listed
});

test("policies: an existing policy's dry-run is scoped to the pods that run it", async ({ page }) => {
  const t = new Date().toISOString();
  const evs = [
    { seq: 3, time: t, pod: "onprod", kind: "request", upstream: "blocked.example", method: "GET" }, // a prod pod -> counts
    { seq: 2, time: t, pod: "other", kind: "request", upstream: "blocked.example", method: "GET" },  // NOT a prod pod -> must be ignored
  ];
  await page.route(/\/v1\/pods(\?|$)/, (r) => r.fulfill({ json: [
    { name: "onprod", state: "running", size: "weak", mode: "headless", policy: "prod", autoscale: false, cpu: "1%", memPerc: "1%", mem: "" },
    { name: "other", state: "running", size: "weak", mode: "headless", policy: "readonly", autoscale: false, cpu: "1%", memPerc: "1%", mem: "" },
  ] }));
  await page.route(/\/v1\/policies(\/|\?|$)/, (r) => r.fulfill({ json: [
    { name: "prod", egress: "redact", allow_upstreams: [], deny_upstreams: ["blocked.example"], methods: {} },
    { name: "readonly", egress: "redact", allow_upstreams: [], deny_upstreams: [], methods: {} },
  ] }));
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: true, brokenAt: 0 } }));
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 204, body: "" }));
  await page.route(/\/v1\/audit(\?|$)/, (r) => r.fulfill({ json: evs }));
  await page.goto("/policies/prod");

  // Only onprod runs prod, so only its request to the deny-listed host counts —
  // "other" hits the same host but is ignored. 1 denied, not 2.
  await expect(page.locator(".dryrun__title")).toContainText("1 pod on this policy");
  await expect(page.locator(".dryrun")).toContainText("1 would be denied");
  await expect(page.locator(".dryrun__list li").first()).toContainText("×1");
});

test("policies: the new-policy form is not wiped by background polls", async ({ page }) => {
  await page.route(/\/v1\/policies(\/|\?|$)/, (r) => r.fulfill({ json: [] }));
  await mockAudit(page);
  await mockPods(page); // polled every 3s -> re-renders the policy view
  await page.goto("/policies/new");

  await page.locator("#pol-name").fill("keepme");
  await page.getByRole("button", { name: /Add destination/ }).click();
  await page.locator(".rule__host").first().fill("api.example.com");

  // Cross the 3s pod-poll boundary: the draft must survive the re-render.
  await page.waitForTimeout(3600);
  await expect(page.locator("#pol-name")).toHaveValue("keepme");
  await expect(page.locator(".rule__host")).toHaveCount(1);
  await expect(page.locator(".rule__host").first()).toHaveValue("api.example.com");
});

test("policies: flags ungoverned pods and dry-runs the rules against recent traffic", async ({ page }) => {
  // A running pod with no policy (ungoverned) + one governed by "prod".
  await page.route(/\/v1\/pods(\?|$)/, (r) => r.fulfill({ json: [
    { name: "agent1", state: "running", size: "weak", mode: "headless", policy: "prod", autoscale: false, cpu: "10%", memPerc: "20%", mem: "" },
    { name: "loose", state: "running", size: "weak", mode: "interactive", policy: "", autoscale: false, cpu: "5%", memPerc: "9%", mem: "" },
  ] }));
  await page.route(/\/v1\/policies(\/|\?|$)/, (r) => r.fulfill({ json: [
    { name: "prod", egress: "redact", allow_upstreams: ["api.anthropic.com"], deny_upstreams: ["metadata.google.internal"], methods: {} },
  ] }));
  await mockAudit(page); // SEED requests: api.anthropic.com (allowed) x2, metadata.google.internal (deny-listed)
  await page.goto("/policies/prod");

  // Ungoverned banner names the unpoliced running pod; the governed one is not listed.
  await expect(page.locator(".insight")).toContainText("loose");
  await expect(page.locator(".insight")).not.toContainText("agent1");
  // Per-policy usage badge: prod governs 1 running pod.
  await expect(page.locator(".list__meta").first()).toContainText("1 pod");
  // Dry-run replays prod over the audit tail: the deny-listed host is caught.
  await expect(page.locator(".dryrun")).toContainText("would be denied");
  await expect(page.locator(".dryrun__list")).toContainText("metadata.google.internal");
  await expect(page.locator(".dryrun__list")).not.toContainText("api.anthropic.com"); // allowed
});

test("audit: the integrity panel verifies the chain and re-verifies on demand", async ({ page }) => {
  await mockAudit(page); // verify -> { ok: true }
  await mockPods(page);
  await page.goto("/audit");
  await expect(page.locator(".integrity--intact")).toContainText("Audit chain intact");
  await expect(page.locator(".integrity__meta")).toContainText("Last verified");
  await page.getByRole("button", { name: "Re-verify" }).click();
  await expect(page.locator(".integrity--intact")).toBeVisible(); // still intact after a manual re-check
});

test("audit: the integrity panel surfaces a broken chain at its seq", async ({ page }) => {
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: false, brokenAt: 42 } }));
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 204, body: "" }));
  await page.route(/\/v1\/audit(\?|$)/, (r) => r.fulfill({ json: SEED }));
  await mockPods(page);
  await page.goto("/audit");
  await expect(page.locator(".integrity--broken")).toContainText("Chain broken at #42");
});

const RUNNING_POD = [{ name: "agent1", state: "running", size: "strong", mode: "headless", policy: "prod", autoscale: false, cpu: "10%", memPerc: "20%", mem: "1GB" }];
const TWO_POLICIES = [
  { name: "prod", egress: "redact", allow_upstreams: ["api.anthropic.com"], deny_upstreams: [], methods: {} },
  { name: "staging", egress: "block", allow_upstreams: ["api.internal"], deny_upstreams: [], methods: {} },
];

test("pod controls: rebinds a policy to a running pod (confirmed, real body POSTed)", async ({ page }) => {
  await page.route(/\/v1\/pods(\?|$)/, (r) => r.fulfill({ json: RUNNING_POD }));
  await page.route(/\/v1\/policies(\?|$)/, (r) => r.fulfill({ json: TWO_POLICIES }));
  let bound: { name?: string } | null = null;
  await page.route("**/v1/pods/agent1/policy", async (r) => { bound = JSON.parse(r.request().postData() || "{}"); return r.fulfill({ status: 204, body: "" }); });
  await mockAudit(page);
  await page.goto("/pods/agent1");

  await expect(page.locator(".chip--on")).toContainText("prod"); // current, disabled
  await page.getByRole("button", { name: "staging" }).click(); // pick a different policy
  await expect(page.locator(".controls__confirm")).toContainText("Bind policy");
  await page.getByRole("button", { name: "Bind", exact: true }).click();
  await expect(page.locator(".controls__status.ok")).toContainText("Now governed by staging");
  expect(bound?.name).toBe("staging"); // the full policy definition was posted
});

test("toasts: a live deny streams in as an alert linking to the audit", async ({ page }) => {
  const ev = JSON.stringify({ seq: 999, time: new Date().toISOString(), pod: "agent1", kind: "request", decision: "deny", upstream: "metadata.google.internal" });
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: true, brokenAt: 0 } }));
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 200, headers: { "content-type": "text/event-stream", "cache-control": "no-cache" }, body: `data: ${ev}\n\n` }));
  await page.route(/\/v1\/audit(\?|$)/, (r) => r.fulfill({ json: [] }));
  await mockPods(page);
  await page.goto("/overview");

  const toast = page.locator(".toast").first();
  await expect(toast).toBeVisible();
  await expect(toast).toContainText("deny");
  await expect(toast).toContainText("metadata.google.internal");
});

test("overview: the egress-window filter narrows the cards", async ({ page }) => {
  const iso = (secAgo: number) => new Date(Date.now() - secAgo * 1000).toISOString();
  const evs = [
    { seq: 3, time: iso(60), pod: "a", kind: "request", upstream: "api.anthropic.com", decision: "allow" },
    { seq: 2, time: iso(120), pod: "a", kind: "request", upstream: "api.github.com", decision: "redact", detail: "redacted 1 secret" },
    { seq: 1, time: iso(3000), pod: "a", kind: "request", upstream: "api.anthropic.com", decision: "allow" }, // > 15m ago
  ];
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: true, brokenAt: 0 } }));
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 204, body: "" }));
  await page.route(/\/v1\/audit(\?|$)/, (r) => r.fulfill({ json: evs }));
  await mockPods(page);
  await page.goto("/overview");

  const requests = page.locator(".card", { hasText: "requests" }).locator(".card__num");
  await expect(requests).toHaveText("3"); // All
  await page.getByRole("radiogroup", { name: "overview time range" }).getByRole("radio", { name: "15m", exact: true }).click();
  await expect(requests).toHaveText("2"); // only the two recent ones remain
});

test("command palette: opens with ctrl+k, filters, navigates, and closes on escape", async ({ page }) => {
  await mockAudit(page);
  await mockPods(page);
  await page.route(/\/v1\/policies(\?|$)/, (r) => r.fulfill({ json: [] }));
  await page.goto("/overview");

  await page.keyboard.press("Control+k");
  await expect(page.locator(".cmdk")).toBeVisible();

  // Filter to a view and activate it -> navigates and closes.
  await page.locator(".cmdk__input").fill("audit");
  const item = page.locator(".cmdk__item", { hasText: "Audit" });
  await expect(item).toBeVisible();
  await item.click();
  await expect(page).toHaveURL(/\/audit$/);
  await expect(page.locator(".cmdk")).toHaveCount(0);

  // Reopen and Escape closes it.
  await page.keyboard.press("Control+k");
  await expect(page.locator(".cmdk")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator(".cmdk")).toHaveCount(0);
});

test("chrome: sidebar collapses/expands (persisted) and the tab title tracks the route", async ({ page }) => {
  await mockAudit(page);
  await mockPods(page);
  await page.goto("/overview");
  await expect(page).toHaveTitle(/poddle . Overview/);
  await expect(page.locator(".app")).not.toHaveClass(/app--collapsed/);

  // Collapse; nav links stay reachable by accessible name even when labels hide.
  await page.getByRole("button", { name: "Collapse sidebar" }).click();
  await expect(page.locator(".app")).toHaveClass(/app--collapsed/);
  await expect(page.getByRole("link", { name: "Pods" })).toBeVisible();

  // Persists across a reload (localStorage).
  await page.reload();
  await expect(page.locator(".app")).toHaveClass(/app--collapsed/);

  // Expand again, and the tab title follows navigation.
  await page.getByRole("button", { name: "Expand sidebar" }).click();
  await expect(page.locator(".app")).not.toHaveClass(/app--collapsed/);
  await page.getByRole("link", { name: "Audit" }).click();
  await expect(page).toHaveTitle(/poddle . Audit/);
});

test("destinations: aggregates egress by host, flags denials, and drills into the audit", async ({ page }) => {
  await mockAudit(page); // SEED: api.anthropic.com (allow + redact), metadata.google.internal (deny)
  await mockPods(page);
  await page.goto("/destinations");

  // Aggregated rows; a deny-listed host carries the flag, an allowed one does not.
  await expect(page.locator("tbody tr")).toHaveCount(2); // evil.example is kind "block", not a request
  await expect(page.locator("tr", { hasText: "metadata.google.internal" }).locator(".dest__flag")).toBeVisible();
  await expect(page.locator("tr", { hasText: "api.anthropic.com" }).locator(".dest__flag")).toHaveCount(0);

  // Filter narrows the list.
  await page.getByPlaceholder("Filter destinations").fill("metadata");
  await expect(page.locator("tbody tr")).toHaveCount(1);
  await page.getByPlaceholder("Filter destinations").fill("");

  // Clicking a destination drills into the audit filtered to that host.
  await page.locator("tr", { hasText: "api.anthropic.com" }).first().click();
  await expect(page).toHaveURL(/\/audit\?q=api\.anthropic\.com/);
  await expect(page.locator("table")).toContainText("api.anthropic.com");
  await expect(page.locator("table")).not.toContainText("metadata.google.internal");
});

test("pod controls: revokes credentials on a running pod (confirmed)", async ({ page }) => {
  await page.route(/\/v1\/pods(\?|$)/, (r) => r.fulfill({ json: RUNNING_POD }));
  await page.route(/\/v1\/policies(\?|$)/, (r) => r.fulfill({ json: TWO_POLICIES }));
  let revoked = false;
  await page.route("**/v1/pods/agent1", async (r) => {
    if (r.request().method() === "DELETE") { revoked = true; return r.fulfill({ status: 204, body: "" }); }
    return r.fallback();
  });
  await mockAudit(page);
  await page.goto("/pods/agent1");

  await page.getByRole("button", { name: "Revoke credentials" }).click();
  await expect(page.locator(".controls__confirm")).toContainText("Revoke every credential");
  await page.getByRole("button", { name: "Revoke", exact: true }).click();
  await expect(page.locator(".controls__status.ok")).toContainText("Credentials revoked");
  expect(revoked).toBe(true);
});

// ---- resilience: the console must survive a hostile / failing daemon ----
test("resilience: every view renders a graceful empty state when the API errors", async ({ page }) => {
  await page.route("**/v1/**", (r) => r.fulfill({ status: 500, contentType: "text/plain", body: "boom" }));
  await page.goto("/overview");
  await expect(page.locator(".sidebar")).toBeVisible();
  await expect(page.locator(".cards")).toBeVisible(); // rendered the real (empty) view, not stuck on skeletons
  await expect(page.locator(".skel")).toHaveCount(0);

  await page.getByRole("link", { name: "Pods" }).click();
  await expect(page.locator("main")).toContainText("No pods running yet");
  await page.getByRole("link", { name: "Destinations" }).click();
  await expect(page.locator(".panel.empty")).toBeVisible();
  await page.getByRole("link", { name: "Policies" }).click();
  await expect(page.locator(".editor.empty")).toBeVisible();
});

test("resilience: coerces non-array API responses instead of crashing", async ({ page }) => {
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: true, brokenAt: 0 } }));
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 204, body: "" }));
  await page.route(/\/v1\/audit(\?|$)/, (r) => r.fulfill({ json: { error: "nope" } })); // object, not a list
  await page.route(/\/v1\/pods(\?|$)/, (r) => r.fulfill({ json: 42 }));                   // a number
  await page.route(/\/v1\/policies(\/|\?|$)/, (r) => r.fulfill({ json: null }));           // null
  await page.goto("/overview");
  await expect(page.locator(".cards")).toBeVisible();
  await expect(page.locator(".card", { hasText: "requests" }).locator(".card__num")).toHaveText("0");
  await page.goto("/policies");
  await expect(page.locator(".layout")).toBeVisible(); // a null policy list does not blow up the view
});

test("resilience: a failed policy rebind reports an error, not a false success", async ({ page }) => {
  await page.route(/\/v1\/pods(\?|$)/, (r) => r.fulfill({ json: [
    { name: "agent1", state: "running", size: "weak", mode: "headless", policy: "prod", autoscale: false, cpu: "1%", memPerc: "1%", mem: "" },
  ] }));
  await page.route(/\/v1\/policies(\?|$)/, (r) => r.fulfill({ json: [
    { name: "prod", egress: "redact", allow_upstreams: [], deny_upstreams: [], methods: {} },
    { name: "staging", egress: "redact", allow_upstreams: [], deny_upstreams: [], methods: {} },
  ] }));
  await page.route("**/v1/pods/agent1/policy", (r) => r.fulfill({ status: 500, body: "boom" }));
  await mockAudit(page);
  await page.goto("/pods/agent1");
  await page.getByRole("button", { name: "staging" }).click();
  await page.getByRole("button", { name: "Bind", exact: true }).click();
  await expect(page.locator(".controls__status.bad")).toContainText("Could not bind");
});

test("resilience: a failed policy save surfaces the error and does not navigate", async ({ page }) => {
  await page.route(/\/v1\/policies(\/|\?|$)/, (r) => (r.request().method() === "PUT"
    ? r.fulfill({ status: 500, body: "boom" })
    : r.fulfill({ json: [] })));
  await mockAudit(page);
  await mockPods(page);
  await page.goto("/policies/new");
  await page.locator("#pol-name").fill("failpol");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.locator(".err")).toContainText("Save failed");
  await expect(page).toHaveURL(/\/policies\/new$/);
});

test("command palette: shows a no-matches state", async ({ page }) => {
  await mockAudit(page);
  await mockPods(page);
  await page.route(/\/v1\/policies(\?|$)/, (r) => r.fulfill({ json: [] }));
  await page.goto("/overview");
  await page.keyboard.press("Control+k");
  await page.locator(".cmdk__input").fill("zzz-nothing-here");
  await expect(page.locator(".cmdk__empty")).toContainText("No matches");
});

test("resilience: deep-linking a nonexistent pod shows 'not running', not a crash", async ({ page }) => {
  await mockPods(page); // agent1 + agent2, no "ghost"
  await mockAudit(page);
  await page.goto("/pods/ghost");
  await expect(page.locator(".detail-title")).toHaveText("ghost");
  await expect(page.locator(".detail-head")).toContainText("not running");
});

// ---- more interaction & data-integrity coverage ----
test("audit: CSV export escapes commas, quotes, and newlines", async ({ page }) => {
  const evs = [{ seq: 1, time: new Date().toISOString(), pod: "p", kind: "request", decision: "deny", upstream: "x.example", method: "GET", status: 403, detail: 'blocked, "quoted"\nsecond line' }];
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: true, brokenAt: 0 } }));
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 204, body: "" }));
  await page.route(/\/v1\/audit(\?|$)/, (r) => r.fulfill({ json: evs }));
  await mockPods(page);
  await page.goto("/audit");
  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: "Export CSV" }).click(),
  ]);
  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const c of stream) chunks.push(c as Buffer);
  const csv = Buffer.concat(chunks).toString("utf8");
  expect(csv.split("\n")[0]).toContain("seq,time,pod");            // seq leads the header
  expect(csv).toContain('"blocked, ""quoted""\nsecond line"');    // quoted + doubled quotes + embedded newline
});

test("command palette: arrow keys move the selection and Enter activates it", async ({ page }) => {
  await mockAudit(page);
  await mockPods(page);
  await page.route(/\/v1\/policies(\?|$)/, (r) => r.fulfill({ json: [] }));
  await page.goto("/overview");
  await page.keyboard.press("Control+k");
  await expect(page.locator(".cmdk")).toBeVisible();
  await page.locator(".cmdk__input").focus();
  await expect(page.locator(".cmdk__item.on")).toContainText("Overview");
  await page.keyboard.press("ArrowDown");
  await expect(page.locator(".cmdk__item.on")).toContainText("Pods");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/pods$/);
});

test("audit: the decision filter responds to arrow keys", async ({ page }) => {
  await mockAudit(page);
  await mockPods(page);
  await page.goto("/audit");
  const group = page.getByRole("radiogroup", { name: "filter by decision" });
  await group.getByRole("radio", { name: "All", exact: true }).focus();
  await page.keyboard.press("ArrowRight"); // All -> Allow (moves AND selects)
  await expect(group.getByRole("radio", { name: "Allow", exact: true })).toHaveAttribute("aria-checked", "true");
  await expect(page.locator("table")).toContainText("api.anthropic.com");
  await expect(page.locator("table")).not.toContainText("evil.example"); // a block, filtered out
});

test("policies: the dry-run honours a '.suffix' subdomain allow rule", async ({ page }) => {
  const t = new Date().toISOString();
  const evs = [
    { seq: 2, time: t, pod: "a", kind: "request", upstream: "api.github.com", method: "GET" }, // matches .github.com
    { seq: 1, time: t, pod: "a", kind: "request", upstream: "evil.example", method: "GET" },    // no match -> denied
  ];
  await page.route("**/v1/audit/verify", (r) => r.fulfill({ json: { ok: true, brokenAt: 0 } }));
  await page.route("**/v1/audit/stream", (r) => r.fulfill({ status: 204, body: "" }));
  await page.route(/\/v1\/audit(\?|$)/, (r) => r.fulfill({ json: evs }));
  await page.route(/\/v1\/policies(\/|\?|$)/, (r) => r.fulfill({ json: [] }));
  await mockPods(page);
  await page.goto("/policies/new");
  await page.getByRole("button", { name: /Add destination/ }).click();
  await page.locator(".rule__host").first().fill(".github.com");
  await expect(page.locator(".dryrun")).toContainText("1 would be denied");
  await expect(page.locator(".dryrun__list")).toContainText("evil.example");
  await expect(page.locator(".dryrun__list")).not.toContainText("api.github.com"); // allowed via subdomain
});

test("pod controls: Cancel dismisses a confirm without mutating", async ({ page }) => {
  await page.route(/\/v1\/pods(\?|$)/, (r) => r.fulfill({ json: [
    { name: "agent1", state: "running", size: "weak", mode: "headless", policy: "prod", autoscale: false, cpu: "1%", memPerc: "1%", mem: "" },
  ] }));
  await page.route(/\/v1\/policies(\?|$)/, (r) => r.fulfill({ json: [
    { name: "prod", egress: "redact", allow_upstreams: [], deny_upstreams: [], methods: {} },
    { name: "staging", egress: "redact", allow_upstreams: [], deny_upstreams: [], methods: {} },
  ] }));
  let called = false;
  await page.route("**/v1/pods/agent1/policy", (r) => { called = true; return r.fulfill({ status: 204, body: "" }); });
  await mockAudit(page);
  await page.goto("/pods/agent1");
  await page.getByRole("button", { name: "staging" }).click();
  await expect(page.locator(".controls__confirm")).toBeVisible();
  await page.getByRole("button", { name: "Cancel" }).click();
  await expect(page.locator(".controls__confirm")).toHaveCount(0);
  expect(called).toBe(false);
});
