// Regenerates the dashboard audit screenshot from the REAL built SPA + the seeded
// audit data, into BOTH the README asset and the marketing-site public dir — one
// generator, one source of truth. Deterministic: the clock is frozen (shot-serve
// injects it) and motion is reduced. Run via `task assets` (after `npm ci` +
// `npx playwright install chromium`), or directly: node tests/screenshot.mjs
import { chromium } from "@playwright/test";
import { startServer } from "./shot-serve.mjs";
import { fileURLToPath } from "node:url";
import { dirname, join, relative } from "node:path";
import { copyFileSync } from "node:fs";

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = join(HERE, "..", "..", "..", ".."); // src/web/dashboard/tests -> repo root
const OUT = [
  join(ROOT, ".github", "assets", "dashboard-audit.png"),
  join(ROOT, "src", "web", "site", "public", "dashboard-audit.png"),
];

const { server, port } = await startServer(0);
const browser = await chromium.launch();
try {
  const page = await browser.newPage({ viewport: { width: 1240, height: 820 }, deviceScaleFactor: 2 });
  await page.emulateMedia({ reducedMotion: "reduce", colorScheme: "dark" });
  await page.goto(`http://127.0.0.1:${port}/audit`, { waitUntil: "domcontentloaded" }); // shot-serve forces data-theme="dark"
  await page.getByText("14 events").first().waitFor({ timeout: 15_000 }); // table populated
  await page.waitForTimeout(500); // let fonts + layout settle
  await page.screenshot({ path: OUT[0], fullPage: true, animations: "disabled" });
  for (const p of OUT.slice(1)) copyFileSync(OUT[0], p);
  console.error("wrote " + OUT.map((p) => relative(ROOT, p).replace(/\\/g, "/")).join(" , "));
} finally {
  await browser.close();
  server.close();
}
