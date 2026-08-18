// Launches the REAL `poddle dashboard` binary (serving the embedded bundle +
// the file-backed /v1/policies) on a temp config dir, so Playwright drives the
// actual shipped artifact. Cross-platform.
import { spawn } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const isWin = process.platform === "win32";
const bin =
  process.env.PODDLE_BIN ||
  join(process.cwd(), "..", "..", "..", "bin", isWin ? "poddle.exe" : "poddle");
const port = process.env.DASH_PORT || "5099";
const cfg = mkdtempSync(join(tmpdir(), "poddle-ui-"));

const child = spawn(bin, ["dashboard", "--port", port], {
  stdio: "inherit",
  env: { ...process.env, XDG_CONFIG_HOME: cfg, XDG_RUNTIME_DIR: cfg },
});
child.on("exit", (code) => process.exit(code ?? 0));
for (const sig of ["SIGTERM", "SIGINT"]) process.on(sig, () => child.kill());
