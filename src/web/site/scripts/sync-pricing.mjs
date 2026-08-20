// Inject canonical pricing (from the private poddle-cloud repo) into the site
// build. Runs as `prebuild`, so a normal `npm run build` picks it up.
//
// Resolution order:
//   1. PODDLE_PRICING_SRC - path to a pricing.json file (deploy copies it)
//   2. PODDLE_PRICING_URL - URL serving pricing.json (deploy fetches it)
//   3. neither            - no-op; the committed placeholder pricing.json is used
//
// Output: src/data/pricing.generated.json (gitignored), which the loader prefers.
// A missing source with neither env var set is NOT an error - a standalone or
// open-source build just renders the placeholder.

import { writeFileSync, copyFileSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const out = fileURLToPath(new URL('../src/data/pricing.generated.json', import.meta.url));
const src = process.env.PODDLE_PRICING_SRC;
const url = process.env.PODDLE_PRICING_URL;

try {
  if (src) {
    if (!existsSync(src)) throw new Error(`PODDLE_PRICING_SRC not found: ${src}`);
    copyFileSync(src, out);
    console.log('[sync-pricing] copied canonical pricing -> pricing.generated.json');
  } else if (url) {
    try {
      const res = await fetch(url);
      if (!res.ok) throw new Error(`GET ${url} -> ${res.status}`);
      const text = await res.text();
      JSON.parse(text); // validate it is JSON before writing
      writeFileSync(out, text);
      console.log('[sync-pricing] fetched canonical pricing -> pricing.generated.json');
    } catch (fetchErr) {
      // A pricing-fetch blip must not break the marketing deploy: warn and fall
      // back to the committed placeholder rather than failing the build. (A
      // missing PODDLE_PRICING_SRC file below still fails loudly - that is an
      // explicit misconfiguration, not a transient network issue.)
      console.error(`[sync-pricing] WARNING: ${fetchErr.message}; using placeholder pricing.json`);
    }
  } else {
    console.log('[sync-pricing] no PODDLE_PRICING_SRC/URL set; using committed placeholder pricing.json');
  }
} catch (err) {
  console.error(`[sync-pricing] ${err.message}`);
  process.exit(1);
}
