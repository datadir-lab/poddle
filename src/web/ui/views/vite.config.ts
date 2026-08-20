import { defineConfig } from "vite";
import preact from "@preact/preset-vite";

// Library build for @poddle/ui/views: a Preact component library consumed by
// the core dashboard and (via the published package) the commercial cloud
// console. preact/preact-hooks stay external — peers, not bundled.
export default defineConfig({
  plugins: [preact()],
  build: {
    lib: {
      entry: "views/index.ts",
      formats: ["es"],
      fileName: () => "index.js",
    },
    outDir: "views/dist",
    emptyOutDir: true,
    rollupOptions: {
      external: ["preact", "preact/hooks"],
    },
  },
});
