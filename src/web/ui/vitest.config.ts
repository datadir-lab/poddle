import { defineConfig } from "vite";
import preact from "@preact/preset-vite";

// Unit tests for @poddle/ui/views (Vitest + jsdom). Separate from
// views/vite.config.ts (the library build) so `npm test` doesn't need the
// external-preact/lib-mode settings.
export default defineConfig({
  plugins: [preact()],
  test: {
    environment: "jsdom",
    include: ["views/**/*.test.{ts,tsx}"],
  },
});
