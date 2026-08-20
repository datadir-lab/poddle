// Generate the terminal demo SVG and place it in BOTH the README asset dir and
// the marketing-site public dir (one source of truth). Cross-platform (pure
// Node, no shell quoting). Used by `task assets` and the lefthook pre-commit hook.
import { execFileSync } from "node:child_process";
import { copyFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = join(HERE, "..", ".."); // docs/demo -> repo root
const README = join(ROOT, ".github", "assets", "demo.svg");
const SITE = join(ROOT, "src", "web", "site", "public", "demo.svg");

execFileSync(process.execPath, [join(HERE, "gen-svg.js"), README], { stdio: "inherit" });
copyFileSync(README, SITE);
console.error("demo.svg -> .github/assets/ + src/web/site/public/");
