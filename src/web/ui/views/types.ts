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

export type Grouped = { pod: string; decision: string; upstream: string; count: number; secrets: number };

export type SegOption = { value: string; label: string; tone?: string };

export type Stats = {
  pods: number;
  requests: number;
  redactions: number;
  secrets: number;
  blocked: number;
  denied: number;
};
