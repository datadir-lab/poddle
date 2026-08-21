// Shared types for @poddle/ui/views. Canonical shapes moved verbatim from
// src/web/dashboard/src/main.tsx (the poddle daemon's /v1 API responses) plus
// the presentational types derived from the dashboard's view layer.

export type Event = {
  seq: number; time: string; source?: string; pod?: string;
  kind: string; upstream?: string; method?: string; path?: string;
  status?: number; decision?: string; detail?: string;
};
export type Policy = {
  name: string; allow_upstreams?: string[]; deny_upstreams?: string[];
  methods?: Record<string, string[]>; egress?: string;
};
export type Pod = {
  name: string; state: string; size: string; mode: string; policy: string;
  autoscale: boolean; cpu: string; memPerc: string; mem: string;
};

export type Hist = Record<string, { cpu: number[]; mem: number[] }>;

export type Verify = { ok: boolean; brokenAt: number } | null;

// The audit stream's live connection status (initial connect, streaming, or
// reconnecting after a drop).
export type Conn = "connecting" | "live" | "down";

export type Grouped = { pod: string; decision: string; upstream: string; count: number; secrets: number };

export type SegOption = { value: string; label: string; tone?: string; badge?: string | number };

export type Stats = {
  pods: number;
  requests: number;
  redactions: number;
  secrets: number;
  blocked: number;
  denied: number;
};

export type Dest = {
  upstream: string; total: number; allow: number; redact: number;
  deny: number; block: number; secrets: number; pods: Set<string>;
};

// ---- presentational / UI-only shapes (no daemon API counterpart) ----

// A single allow-list row in the visual policy builder: a host plus an
// optional set of methods it is restricted to (empty = any method). `open`
// reveals the method toggles even before any are picked.
export type AllowRow = { host: string; methods: string[]; open: boolean };

// A starter template for the policy builder: a labelled, hinted preset the
// operator can apply to a blank new policy (the container owns the concrete
// template set; the editor just renders and applies whichever are passed in).
export type PolicyTemplate = { id: string; label: string; hint: string; policy: Omit<Policy, "name"> };

// One aggregated "would be denied" row from a policy dry-run.
export type DryRow = { upstream: string; method: string; reason: string; count: number };

// A single entry in the command palette (⌘K).
export type Cmd = { id: string; label: string; hint: string; icon: string; run: () => void };

// A live denial/block surfaced the moment it streams in.
export type Toast = { id: number; pod: string; decision: string; upstream: string };

// A pending confirmation for a destructive/consequential pod action.
export type Pending = { type: "bind"; name: string } | { type: "revoke" } | null;

export const HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];

// Shared time-range filter options (the audit feed's "time range" and the
// overview's "egress window" both filter events by the same buckets).
export const TIME_RANGES: SegOption[] = [
  { value: "", label: "All" },
  { value: "15m", label: "15m" },
  { value: "1h", label: "1h" },
  { value: "24h", label: "24h" },
];
export const RANGE_MS: Record<string, number> = { "15m": 900000, "1h": 3600000, "24h": 86400000 };
