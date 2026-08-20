#!/bin/sh
# Guardrail: keep the README + marketing-site visual assets from going stale.
# Regenerate the deterministic demo SVG and compare; for the browser-rendered
# dashboard screenshot (not byte-stable across machines) require that it was
# refreshed whenever the dashboard UI changed. Fix locally with: task assets
# Usage: sh scripts/check-assets.sh [base-ref]   (base-ref defaults to origin/main)
set -eu
base="${1:-origin/main}"
fail=0

# --- Terminal demo SVG: pure Node output, so regenerate and byte-compare. ---
node docs/demo/gen-svg.js /tmp/_demo.svg
if ! cmp -s /tmp/_demo.svg .github/assets/demo.svg; then
  echo "::error file=.github/assets/demo.svg::demo.svg is stale — run 'task assets' and commit the result."
  fail=1
fi
if ! cmp -s .github/assets/demo.svg src/web/site/public/demo.svg; then
  echo "::error file=src/web/site/public/demo.svg::the site copy of demo.svg is out of sync — run 'task assets'."
  fail=1
fi

# --- Dashboard screenshot: browser-rendered (font AA differs across machines),
#     so guard by change-coupling: if the dashboard UI changed, the shot must too. ---
touched() { ! git diff --quiet "$base"...HEAD -- "$@"; }
if touched src/web/dashboard/src; then
  if ! touched .github/assets/dashboard-audit.png; then
    echo "::error file=.github/assets/dashboard-audit.png::the dashboard changed but its audit screenshot was not refreshed — run 'task assets' and commit dashboard-audit.png."
    fail=1
  fi
fi

if [ "$fail" -eq 0 ]; then echo "README + site assets are up to date."; fi
exit "$fail"
