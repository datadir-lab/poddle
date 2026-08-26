// Perceptual compare of two dashboard screenshots — the committed marketing asset
// vs a freshly-rendered one — tolerant of cross-platform font/anti-aliasing
// differences (the render is deterministic within a platform, but Linux vs Windows
// chromium anti-alias text slightly differently). Fails only if the *fraction* of
// meaningfully-different pixels exceeds SHOT_DIFF_THRESHOLD, which a real visual
// change blows past but AA noise stays well under.
//
// Usage: node tests/compare-shot.mjs <committed.png> <fresh.png>
import { readFileSync } from "node:fs";
import { PNG } from "pngjs";
import pixelmatch from "pixelmatch";

const THRESHOLD = Number(process.env.SHOT_DIFF_THRESHOLD ?? "0.05"); // max fraction of differing pixels
const [committedPath, freshPath] = process.argv.slice(2);
if (!committedPath || !freshPath) {
  console.error("usage: node tests/compare-shot.mjs <committed.png> <fresh.png>");
  process.exit(2);
}

const a = PNG.sync.read(readFileSync(committedPath));
const b = PNG.sync.read(readFileSync(freshPath));

if (a.width !== b.width || a.height !== b.height) {
  console.error(
    `::error::dashboard screenshot dimensions changed (${a.width}x${a.height} committed vs ${b.width}x${b.height} rendered) — a real layout change. Run 'task assets' and commit the refreshed screenshot.`,
  );
  process.exit(1);
}

// per-pixel threshold 0.1 = ignore small AA-level colour deltas; count only pixels
// that differ meaningfully.
const differing = pixelmatch(a.data, b.data, null, a.width, a.height, { threshold: 0.1 });
const total = a.width * a.height;
const frac = differing / total;
console.error(
  `dashboard screenshot perceptual diff: ${differing}/${total}px = ${(frac * 100).toFixed(2)}% ` +
    `(threshold ${(THRESHOLD * 100).toFixed(1)}%)`,
);

if (frac > THRESHOLD) {
  console.error(
    "::error::dashboard-audit.png looks stale — the dashboard changed visually beyond AA noise. Run 'task assets' and commit the refreshed screenshot.",
  );
  process.exit(1);
}
console.error("dashboard screenshot is current (within AA tolerance).");
