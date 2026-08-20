// Minimal static server for the built Astro site (dist/), used as the Playwright
// webServer. No deps, cross-platform. Astro emits each route as <path>/index.html,
// with real files for assets and /rss.xml, /robots.txt, etc.
import http from "node:http";
import { readFile, stat } from "node:fs/promises";
import { join, extname, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const DIST = fileURLToPath(new URL("../../dist", import.meta.url)); // tests/e2e -> site/dist
const PORT = Number(process.env.SITE_PORT || 4319);

const TYPES = {
  ".html": "text/html; charset=utf-8", ".css": "text/css", ".js": "text/javascript",
  ".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".ico": "image/x-icon",
  ".xml": "application/xml; charset=utf-8", ".json": "application/json", ".txt": "text/plain; charset=utf-8",
  ".woff2": "font/woff2", ".woff": "font/woff", ".webmanifest": "application/manifest+json",
};

async function resolveFile(urlPath) {
  const raw = decodeURIComponent(urlPath.split("?")[0]);
  // Sanitize against ../ path traversal: normalize, then strip any leading "../"
  // segments so the joined path can never escape DIST (the recommended pattern).
  const clean = normalize(raw).replace(/^(\.\.([/\\]|$))+/, "");
  const candidates = extname(clean)
    ? [join(DIST, clean)]                                            // asset or /rss.xml
    : [
        join(DIST, clean, "index.html"),                            // route directory
        join(DIST, clean.replace(/[/\\]$/, "") + ".html"),          // flat .html fallback
      ];
  for (const f of candidates) {
    try { if ((await stat(f)).isFile()) return f; } catch { /* try next */ }
  }
  return null;
}

const server = http.createServer(async (req, res) => {
  let file = await resolveFile(req.url);
  let status = 200;
  if (!file) { file = join(DIST, "404.html"); status = 404; }
  try {
    const body = await readFile(file);
    res.writeHead(status, { "Content-Type": TYPES[extname(file)] || "application/octet-stream" });
    res.end(body);
  } catch {
    res.writeHead(status === 404 ? 404 : 500);
    res.end(status === 404 ? "Not found" : "Server error");
  }
});
server.listen(PORT, () => console.log(`site preview on http://localhost:${PORT}`));
