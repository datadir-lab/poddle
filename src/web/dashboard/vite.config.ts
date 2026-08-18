import { defineConfig } from "vite";
import preact from "@preact/preset-vite";

// The built bundle is emitted into the Go package that go:embed's it, so
// `poddle dashboard` ships it in the binary (committed via `task dashboard-build`).
export default defineConfig({
  plugins: [preact()],
  build: {
    outDir: "../../cli/dashboard/dist",
    emptyOutDir: true,
  },
});
