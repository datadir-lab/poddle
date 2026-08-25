import { test, expect } from "@playwright/test";

test.describe("homepage", () => {
  test("hero leads with the headline and a real terminal", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByRole("heading", { level: 1 })).toContainText("leak out of");
    // the terminal shows a real poddle session
    await expect(page.locator(".term__body")).toContainText("poddle up my-sandbox");
    // both hero CTAs
    const hero = page.locator(".hero");
    await expect(hero.getByRole("link", { name: "Start free" })).toBeVisible();
    await expect(hero.getByRole("link", { name: /Self-host it/ })).toBeVisible();
  });

  test("shows the dark audit-dashboard screenshot section", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Provable after the fact." })).toBeVisible();
    // Scope to the audit-dashboard shot: the homepage now has two .shot__img figures
    // (the terminal demo and this one), so the bare class matches 2 under strict mode.
    await expect(page.locator('img.shot__img[src="/dashboard-audit.png"]')).toBeVisible();
  });

  test('"How it works" is a click-driven tabset (no auto-rotation)', async ({ page }) => {
    await page.goto("/");
    // First panel is active by default.
    await expect(page.locator(".flows__panel.is-active")).toContainText("poddle identity add work");
    // Clicking another tab switches the panel.
    await page.getByRole("tab", { name: "Headless task" }).click();
    await expect(page.locator(".flows__panel.is-active")).toContainText("poddle task");
    await expect(page.getByRole("tab", { name: "Headless task" })).toHaveClass(/is-active/);
    // Arrow keys move between tabs (standard tablist behaviour).
    await page.getByRole("tab", { name: "Headless task" }).focus();
    await page.keyboard.press("ArrowRight");
    await expect(page.getByRole("tab", { name: "Remote host" })).toBeFocused();
  });
});

test.describe("navigation", () => {
  test("desktop nav links are present", async ({ page }) => {
    await page.goto("/");
    for (const name of ["Cloud", "Connectors", "Pricing", "Docs", "Blog"]) {
      await expect(page.locator(".nav__links").getByRole("link", { name, exact: true })).toBeVisible();
    }
  });

  test("mobile hamburger opens and closes the menu", async ({ page }) => {
    await page.setViewportSize({ width: 380, height: 800 });
    await page.goto("/");
    const toggle = page.locator(".nav__toggle");
    const links = page.locator(".nav__links");
    await expect(toggle).toBeVisible();
    await expect(links).toBeHidden(); // collapsed on mobile
    await toggle.click();
    await expect(links).toBeVisible();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await toggle.click();
    await expect(links).toBeHidden();
  });
});

test.describe("blog", () => {
  test("index lists the post and links through to it", async ({ page }) => {
    await page.goto("/blog");
    const link = page.getByRole("link", { name: /leak out of/ });
    await expect(link).toBeVisible();
    await link.click();
    await expect(page).toHaveURL(/\/blog\/introducing-poddle\/?$/);
    await expect(page.getByRole("heading", { level: 1 })).toContainText("leak out of");
    await expect(page.locator(".post__body")).toContainText("the real secret never enters the pod");
  });

  test("RSS feed is valid and lists the post", async ({ request }) => {
    const res = await request.get("/rss.xml");
    expect(res.status()).toBe(200);
    expect(res.headers()["content-type"]).toContain("xml");
    const body = await res.text();
    expect(body).toContain("<rss");
    expect(body).toContain("<title>poddle blog</title>");
    expect(body).toContain("/blog/introducing-poddle");
  });
});

test.describe("docs", () => {
  test('pressing "/" opens the search modal', async ({ page }) => {
    await page.goto("/docs");
    await expect(page.locator("#docsearch-modal")).toBeHidden();
    await page.keyboard.press("/");
    await expect(page.locator("#docsearch-modal")).toBeVisible();
  });

  test("a command example renders in a terminal", async ({ page }) => {
    await page.goto("/docs/examples");
    await expect(page.locator(".term__body").first()).toContainText("poddle");
  });
});

test("core routes render", async ({ page }) => {
  for (const path of ["/pricing", "/cloud", "/compare", "/connectors", "/about", "/security", "/terms", "/privacy"]) {
    const res = await page.goto(path);
    expect(res?.status(), `${path} should be OK`).toBeLessThan(400);
    await expect(page.locator("footer.footer")).toBeVisible();
  }
});

test("unknown route serves the 404 page", async ({ page }) => {
  const res = await page.goto("/no-such-page-xyz");
  expect(res?.status()).toBe(404);
});

test("homepage has SEO essentials (title, canonical, OpenGraph)", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveTitle(/poddle/);
  await expect(page.locator('link[rel="canonical"]')).toHaveCount(1);
  await expect(page.locator('meta[property="og:image"]')).toHaveCount(1);
});
