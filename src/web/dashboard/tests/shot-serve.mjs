// A tiny static + mock-API server for the dashboard screenshot: serves the
// committed SPA bundle (src/cli/dashboard/dist) with SPA fallback, and answers
// the audit/pods endpoints from audit-fixture.mjs. Used by screenshot.mjs (and
// runnable standalone: `node shot-serve.mjs [port]`).
import http from "node:http";
import { readFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { join, extname, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { EVENTS, PODS, BASE } from "./audit-fixture.mjs";

// Injected before the app boots so relative times ("7s ago") are computed from a
// fixed clock — makes the screenshot deterministic. BASE is a hardcoded fixture
// timestamp; resolve it to a numeric epoch here so the injected script embeds only
// a number (no string payload can break out of the <script>).
const FREEZE_MS = Number(new Date(BASE));
const FREEZE = `<script>(function(){var F=${FREEZE_MS},R=Date;function D(){return arguments.length?new R(...arguments):new R(F)}D.now=function(){return F};D.parse=R.parse;D.UTC=R.UTC;D.prototype=R.prototype;window.Date=D;})()</script>`;

const HERE = dirname(fileURLToPath(import.meta.url));
const DIST = join(HERE, "..", "..", "..", "cli", "dashboard", "dist"); // src/cli/dashboard/dist
const MIME = {
  ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css",
  ".svg": "image/svg+xml", ".woff2": "font/woff2", ".json": "application/json",
  ".png": "image/png", ".ico": "image/x-icon", ".webmanifest": "application/manifest+json",
};
const sendJSON = (res, obj, status = 200) => {
  res.writeHead(status, { "content-type": "application/json" });
  res.end(JSON.stringify(obj));
};

export function startServer(port = 0) {
  const server = http.createServer(async (req, res) => {
    const p = new URL(req.url, "http://x").pathname;
    if (p === "/v1/audit") return sendJSON(res, EVENTS);
    if (p === "/v1/audit/verify") return sendJSON(res, { ok: true, brokenAt: 0 });
    if (p === "/v1/audit/stream") {
      // hold an open SSE stream so the console shows "Live", not "Reconnecting"
      res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache", connection: "keep-alive" });
      res.write(": connected\n\n");
      return; // never end
    }
    if (p === "/v1/pods") return sendJSON(res, PODS);
    if (p.startsWith("/v1/")) return sendJSON(res, []); // any other API: empty
    let file = join(DIST, p === "/" ? "index.html" : decodeURIComponent(p).replace(/^\/+/, ""));
    if (!existsSync(file) || !extname(file)) file = join(DIST, "index.html"); // SPA fallback
    try {
      let body = await readFile(file);
      if (extname(file) === ".html") {
        const html = body.toString("utf8")
          .replace(/<html([^>]*)>/i, '<html$1 data-theme="dark">') // force the dark shot
          .replace("<head>", "<head>" + FREEZE);
        body = Buffer.from(html);
      }
      res.writeHead(200, { "content-type": MIME[extname(file)] || "application/octet-stream" });
      res.end(body);
    } catch { res.writeHead(404); res.end("not found"); }
  });
  return new Promise((resolve) =>
    server.listen(port, "127.0.0.1", () => resolve({ server, port: server.address().port })),
  );
}

if (process.argv[1] && process.argv[1].replace(/\\/g, "/").endsWith("shot-serve.mjs")) {
  const port = Number(process.argv[2]) || 5177;
  startServer(port).then(({ port }) =>
    console.error(`shot-serve: http://127.0.0.1:${port}/  (dist: ${DIST})`),
  );
}
