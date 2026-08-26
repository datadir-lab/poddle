#!/bin/sh
# Guardrail: keep the README + marketing-site visual assets from going stale.
# Regenerate the deterministic demo SVG and byte-compare. (The browser-rendered
# dashboard screenshot is verified separately, by a re-render + compare in the
# dashboard-e2e CI job.) Fix locally with: task assets
set -eu
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

# --- Dashboard screenshot: verified by an actual re-render + byte-compare, not by
#     change-coupling — the dashboard-e2e CI job (which already builds poddle and
#     installs chromium) re-renders the shot and fails if the committed PNG is stale.
#     That correctly passes a no-op/invisible dashboard change (identical re-render)
#     while still catching a real visual change whose screenshot wasn't refreshed. ---

if [ "$fail" -eq 0 ]; then echo "README + site assets are up to date."; fi
exit "$fail"
