// Client-side categorization of a pod's egress into legible categories ("what it
// accesses") plus an action layer ("what it does", only where the method was
// visible), and a least-privilege policy suggestion built from what the pod
// actually reached. Pure — derived entirely from the audit spine.
import { matchHost } from "./policy-eval";
import type { Event, Policy } from "./types";

export type Category = { key: string; label: string; patterns: string[] };

// Built-in classifier (dashboard glue, like the policy templates). Ordered; the
// first matching category wins. Unrecognized hosts fall through to "other".
export const CATEGORIES: Category[] = [
  { key: "model", label: "Model API", patterns: ["api.anthropic.com", "api.openai.com", "generativelanguage.googleapis.com", ".aiplatform.googleapis.com"] },
  { key: "registry", label: "Package registry", patterns: [".pypi.org", ".pythonhosted.org", ".npmjs.org", ".crates.io", ".rubygems.org"] },
  { key: "source", label: "Source control", patterns: [".github.com", ".gitlab.com", ".bitbucket.org"] },
  { key: "artifact", label: "Artifact/container", patterns: ["ghcr.io", ".docker.io", ".docker.com"] },
  { key: "metadata", label: "Cloud metadata", patterns: ["169.254.169.254", "metadata.google.internal"] },
  { key: "telemetry", label: "Telemetry", patterns: [".segment.io", ".sentry.io", ".datadoghq.com"] },
];

export function classify(host: string): string {
  for (const c of CATEGORIES) if (matchHost(host, c.patterns)) return c.key;
  return "other";
}

export type CategoryRollup = {
  key: string; label: string; total: number;
  hosts: string[]; methods: string[];
  allow: number; redact: number; deny: number; block: number;
};

export function categorize(events: Event[]): CategoryRollup[] {
  type Acc = CategoryRollup & { _hosts: Set<string>; _methods: Set<string> };
  const labelFor = (k: string) => (k === "other" ? "Other" : CATEGORIES.find((c) => c.key === k)?.label ?? "Other");
  const m = new Map<string, Acc>();
  for (const e of events) {
    if (e.kind !== "request" || !e.upstream) continue;
    const key = classify(e.upstream);
    const r = m.get(key) ?? { key, label: labelFor(key), total: 0, hosts: [], methods: [], allow: 0, redact: 0, deny: 0, block: 0, _hosts: new Set(), _methods: new Set() };
    r.total++;
    r._hosts.add(e.upstream);
    if (e.method && e.method.toUpperCase() !== "CONNECT") r._methods.add(e.method.toUpperCase());
    if (e.decision === "allow") r.allow++;
    else if (e.decision === "redact") r.redact++;
    else if (e.decision === "deny") r.deny++;
    else if (e.decision === "block") r.block++;
    m.set(key, r);
  }
  return [...m.values()]
    .map((r) => ({ key: r.key, label: r.label, total: r.total, hosts: [...r._hosts].sort(), methods: [...r._methods].sort(), allow: r.allow, redact: r.redact, deny: r.deny, block: r.block }))
    .sort((a, b) => (a.key === "other" ? 1 : b.key === "other" ? -1 : b.total - a.total));
}

// suggestPolicy builds a least-privilege policy from the hosts the pod actually
// reached (allow/redact — egress that happened). Denied/blocked hosts are never
// auto-allowed. Per-host methods are set only where a verb was visible.
export function suggestPolicy(events: Event[], name: string): Policy {
  const hosts = new Set<string>();
  const verbs = new Map<string, Set<string>>();
  for (const e of events) {
    if (e.kind !== "request" || !e.upstream) continue;
    if (e.decision !== "allow" && e.decision !== "redact") continue;
    hosts.add(e.upstream);
    if (e.method && e.method.toUpperCase() !== "CONNECT") {
      const s = verbs.get(e.upstream) ?? new Set<string>();
      s.add(e.method.toUpperCase());
      verbs.set(e.upstream, s);
    }
  }
  const methods: Record<string, string[]> = {};
  for (const [h, s] of verbs) if (s.size) methods[h] = [...s].sort();
  return {
    name,
    allow_upstreams: [...hosts].sort(),
    deny_upstreams: ["169.254.169.254", "metadata.google.internal"],
    methods: Object.keys(methods).length ? methods : undefined,
    egress: "redact",
  };
}
