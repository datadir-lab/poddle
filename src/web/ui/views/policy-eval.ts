// ---- client-side policy evaluation ----
// A faithful port of policy.Decide (Go): the deny-list wins, then the allow-list
// (default-deny when non-empty), then per-host method rules; otherwise allow. A
// ".suffix" pattern matches that domain and any subdomain. Keeping this in lock-
// step with the daemon is what makes the dry-run trustworthy.
//
// Moved verbatim from src/web/dashboard/src/main.tsx so both the core
// dashboard and (eventually) the commercial cloud console share one
// implementation.
import type { AllowRow, DryRow, Event, Policy } from "./types";

export function matchHost(host: string, patterns: string[]): boolean {
  for (const p of patterns) {
    if (p === host) return true;
    if (p.startsWith(".") && (host.endsWith(p) || host === p.slice(1))) return true;
  }
  return false;
}

export function methodsFor(methods: Record<string, string[]> | undefined, host: string): string[] | null {
  if (!methods) return null;
  if (host in methods) return methods[host];
  for (const k in methods) {
    if (k.startsWith(".") && (host.endsWith(k) || host === k.slice(1))) return methods[k];
  }
  if ("*" in methods) return methods["*"]; // catch-all: any host without a more specific rule
  return null;
}

export function decide(pol: Policy, host: string, method: string): { allow: boolean; reason: string } {
  if (matchHost(host, pol.deny_upstreams || [])) return { allow: false, reason: "on the deny-list" };
  if ((pol.allow_upstreams || []).length > 0 && !matchHost(host, pol.allow_upstreams || []))
    return { allow: false, reason: "not allow-listed" };
  const allowed = methodsFor(pol.methods, host);
  if (allowed && method && method !== "CONNECT" && !allowed.some((m) => m.toUpperCase() === method.toUpperCase()))
    return { allow: false, reason: method + " not allowed here" };
  return { allow: true, reason: "" };
}

// dryRun replays a (possibly unsaved) policy over the recent request stream and
// reports what its allow/deny rules would decide. Secret redaction depends on
// request payloads, so it is deliberately out of scope — this is access control.
export function dryRun(pol: Policy, events: Event[]): { total: number; denied: number; rows: DryRow[] } {
  const reqs = events.filter((e) => e.kind === "request" && e.upstream);
  const m = new Map<string, DryRow>();
  let denied = 0;
  for (const e of reqs) {
    const d = decide(pol, e.upstream as string, e.method || "");
    if (d.allow) continue;
    denied++;
    const key = `${e.method || ""}|${e.upstream}`;
    const row = m.get(key) || { upstream: e.upstream as string, method: e.method || "", reason: d.reason, count: 0 };
    row.count++;
    m.set(key, row);
  }
  return { total: reqs.length, denied, rows: [...m.values()].sort((a, b) => b.count - a.count) };
}

// toRows expands a stored policy into builder rows (union of the allow-list and
// any hosts that carry method restrictions, so nothing is lost on a round-trip).
export function toRows(p: Policy): AllowRow[] {
  const m = p.methods || {};
  const hosts = [...new Set([...(p.allow_upstreams || []), ...Object.keys(m)])];
  return hosts.map((h) => ({ host: h, methods: m[h] || [], open: false }));
}
