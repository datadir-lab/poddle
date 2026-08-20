// Pure, client-side aggregations derived from the audit event stream. Moved
// verbatim from src/web/dashboard/src/main.tsx so both the core dashboard and
// (eventually) the commercial cloud console share one implementation.
import type { Event, Grouped, Stats } from "./types";

// secretsFrom parses the redacted-secret count out of an event's detail text
// (falls back to 1 when the count isn't present in the message).
const secretsFrom = (detail?: string) => {
  const m = (detail || "").match(/redacted (\d+)/);
  return m ? +m[1] : 1;
};

export function summarise(events: Event[]): Stats {
  const pods = new Set<string>();
  let requests = 0, redactions = 0, secrets = 0, blocked = 0, denied = 0;
  for (const e of events) {
    if (e.pod) pods.add(e.pod);
    if (e.kind === "request") requests++;
    if (e.decision === "redact") { redactions++; secrets += secretsFrom(e.detail); }
    if (e.decision === "block") blocked++;
    if (e.decision === "deny") denied++;
  }
  return { pods: pods.size, requests, redactions, secrets, blocked, denied };
}

export function group(events: Event[], decisions: string[]): Grouped[] {
  const m = new Map<string, Grouped>();
  for (const e of events) {
    if (!e.decision || !decisions.includes(e.decision)) continue;
    const key = `${e.pod || "—"}|${e.decision}|${e.upstream || "—"}`;
    const g = m.get(key) || { pod: e.pod || "—", decision: e.decision, upstream: e.upstream || "—", count: 0, secrets: 0 };
    g.count++;
    if (e.decision === "redact") g.secrets += secretsFrom(e.detail);
    m.set(key, g);
  }
  return [...m.values()].sort((a, b) => b.count - a.count);
}

// cap1 upper-cases only the first letter (leaves identifiers/values intact).
export const cap1 = (s: string) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);
// humanKind turns a dotted event kind into a readable label: "pod.up" -> "Pod up".
export const humanKind = (k: string) => cap1((k || "").replace(/\./g, " "));

// relTime renders an event's age compactly (the absolute time goes in a tooltip).
export function relTime(iso: string): string {
  const s = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 5) return "just now";
  if (s < 60) return s + "s ago";
  const m = Math.floor(s / 60);
  if (m < 60) return m + "m ago";
  const h = Math.floor(m / 60);
  if (h < 24) return h + "h ago";
  return Math.floor(h / 24) + "d ago";
}

// threshTone maps a live % (of the pod's limit) to a severity tone so the
// sparkline carries state, not just shape (Grafana's threshold-colored cells).
export const threshTone = (v: number) => (v >= 85 ? "hot" : v >= 60 ? "warm" : "cool");
