import { defineConfig } from "vite";
import preact from "@preact/preset-vite";

// The built bundle is emitted into the Go package that go:embed's it, so
// `poddle dashboard` ships it in the binary (committed via `task dashboard-build`).
export default defineConfig({
  plugins: [preact()],
  // @poddle/ui (consumed via file:../ui) is a symlinked local package with its
  // own node_modules/preact for its own dev/build/test. Without dedupe, Vite
  // resolves "preact"/"preact/hooks" imports inside the symlinked package's
  // real path to THAT copy instead of this app's, producing two separate
  // Preact module instances in one bundle — hooks called from @poddle/ui/views
  // components then read a `currentComponent` never set by this app's render
  // loop, silently breaking any component there that uses hooks.
  resolve: {
    dedupe: ["preact", "preact/hooks", "preact/jsx-runtime"],
  },
  build: {
    outDir: "../../cli/dashboard/dist",
    emptyOutDir: true,
  },
});
