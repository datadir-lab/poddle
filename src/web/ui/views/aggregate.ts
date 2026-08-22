// Pure, client-side aggregations derived from the audit event stream. Moved
// verbatim from src/web/dashboard/src/main.tsx so both the core dashboard and
// (eventually) the commercial cloud console share one implementation.
import type { Dest, Event, Grouped, Stats } from "./types";

// secretsFrom parses the redacted-secret count out of an event's detail text
// (falls back to 1 when the count isn't present in the message).
export const secretsFrom = (detail?: string) => {
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

// relTime renders an event's age compactly (kept for consumers that want it).
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

// absTime renders an absolute wall-clock time (24-hour). It shows the time alone
// for today's events and prefixes the date otherwise, so a log reads precisely
// without a tooltip. withSeconds=false (charts) drops the seconds.
export function absTime(iso: string, withSeconds = true): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const now = new Date();
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", ...(withSeconds ? { second: "2-digit" } : {}), hour12: false });
  return sameDay ? time : `${d.toLocaleDateString([], { month: "short", day: "numeric" })} ${time}`;
}

// threshTone maps a live % (of the pod's limit) to a severity tone so the
// sparkline carries state, not just shape (Grafana's threshold-colored cells).
export const threshTone = (v: number) => (v >= 85 ? "hot" : v >= 60 ? "warm" : "cool");

// The four egress decisions, in fixed order, each with its status glyph. These
// are *status* colours (reserved, in tokens.css) so they always ship with a
// label + icon, never colour alone.
export const DECISIONS = [
  { key: "allow", label: "Allow", icon: "check" },
  { key: "redact", label: "Redact", icon: "eyeoff" },
  { key: "deny", label: "Deny", icon: "ban" },
  { key: "block", label: "Block", icon: "octagon" },
  { key: "monitor", label: "Monitor", icon: "info" },
] as const;

export function decisionCounts(events: Event[]): Record<string, number> {
  const c: Record<string, number> = { allow: 0, redact: 0, deny: 0, block: 0, monitor: 0 };
  for (const e of events) if (e.decision && e.decision in c) c[e.decision]++;
  return c;
}

// destinations aggregates egress by upstream host (where the agents are
// reaching, derived from the audit) — the Destinations view's data prep.
export function destinations(events: Event[]): Dest[] {
  const m = new Map<string, Dest>();
  for (const e of events) {
    if (e.kind !== "request" || !e.upstream) continue;
    const d = m.get(e.upstream) || { upstream: e.upstream, total: 0, allow: 0, redact: 0, deny: 0, block: 0, secrets: 0, pods: new Set<string>() };
    d.total++;
    if (e.pod) d.pods.add(e.pod);
    switch (e.decision) {
      case "allow": d.allow++; break;
      case "redact": d.redact++; d.secrets += secretsFrom(e.detail); break;
      case "deny": d.deny++; break;
      case "block": d.block++; break;
    }
    m.set(e.upstream, d);
  }
  return [...m.values()].sort((a, b) => b.total - a.total);
}

// rowKey makes a table row keyboard-operable (Enter/Space) when it is
// clickable, matching the native activation keys of the button/link roles.
export function rowKey(onClick: () => void) {
  return (e: KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onClick(); }
  };
}
