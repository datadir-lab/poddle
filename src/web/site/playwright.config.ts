import { defineConfig, devices } from "@playwright/test";

const PORT = process.env.SITE_PORT || "4319";

// e2e for the marketing site: build the static output, serve it, and drive it in
// a real browser. The site is read-only, so tests run fully in parallel.
export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: "list",
  use: {
    baseURL: `http://localhost:${PORT}`,
    trace: "on-first-retry",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "npm run build && node tests/e2e/serve.mjs",
    url: `http://localhost:${PORT}/`,
    reuseExistingServer: !process.env.CI,
    timeout: 180_000, // covers the astro build
  },
});
