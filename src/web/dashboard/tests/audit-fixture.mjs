// Deterministic seed data for the dashboard screenshot (.github/assets +
// src/web/site/public dashboard-audit.png). This is the single source of truth
// for what the shot shows. Times are offsets from a fixed BASE so the render is
// stable when the browser clock is frozen to BASE at screenshot time.
export const BASE = "2026-08-20T10:04:00Z";
const ago = (s) => new Date(Date.parse(BASE) - s * 1000).toISOString();

export const EVENTS = [
  { seq: 14, time: ago(7),   pod: "poddle-task-9f2a", kind: "request", upstream: "api.anthropic.com",        decision: "allow",  detail: "Provider key injected on the wire" },
  { seq: 13, time: ago(15),  pod: "poddle-task-9f2a", kind: "request", upstream: "api.github.com",           decision: "redact", detail: "Redacted 2 secrets from body" },
  { seq: 12, time: ago(24),  pod: "api",              kind: "request", upstream: "api.anthropic.com",        decision: "allow",  detail: "Provider key injected on the wire" },
  { seq: 11, time: ago(32),  pod: "api",              kind: "request", upstream: "metadata.google.internal", decision: "deny",   detail: "Not in allowlist" },
  { seq: 10, time: ago(39),  pod: "my-sandbox",       kind: "request", upstream: "registry.npmjs.org",       decision: "allow",  detail: "Brokered connector" },
  { seq: 9,  time: ago(47),  pod: "poddle-task-9f2a", kind: "request", upstream: "api.anthropic.com",        decision: "redact", detail: "Redacted 1 secret from body" },
  { seq: 8,  time: ago(56),  pod: "docs-build",       kind: "request", upstream: "api.github.com",           decision: "allow",  detail: "Brokered connector" },
  { seq: 7,  time: ago(64),  pod: "api",              kind: "block",   upstream: "169.254.169.254",          decision: "block",  detail: "Egress blocked by policy" },
  { seq: 6,  time: ago(72),  pod: "my-sandbox",       kind: "request", upstream: "api.anthropic.com",        decision: "allow",  detail: "Provider key injected on the wire" },
  { seq: 5,  time: ago(80),  pod: "poddle-task-9f2a", kind: "request", upstream: "api.github.com",           decision: "redact", detail: "Redacted 3 secrets from body" },
  { seq: 4,  time: ago(88),  pod: "api",              kind: "request", upstream: "metadata.google.internal", decision: "deny",   detail: "Not in allowlist" },
  { seq: 3,  time: ago(96),  pod: "docs-build",       kind: "request", upstream: "registry.npmjs.org",       decision: "allow",  detail: "Brokered connector" },
  { seq: 2,  time: ago(110), pod: "poddle-task-9f2a", kind: "request", upstream: "api.anthropic.com",        decision: "allow",  detail: "Provider key injected on the wire" },
  { seq: 1,  time: ago(125), pod: "api",              kind: "request", upstream: "api.internal.corp",        decision: "redact", detail: "Redacted 1 secret from body" },
];

export const PODS = [
  { name: "poddle-task-9f2a", state: "running", size: "weak",   mode: "headless",    policy: "prod", autoscale: true,  cpu: "12.5%", memPerc: "68%", mem: "2.7GB / 4GB" },
  { name: "api",              state: "running", size: "strong", mode: "interactive", policy: "prod", autoscale: false, cpu: "3.1%",  memPerc: "22%", mem: "1.1GB / 8GB" },
];
