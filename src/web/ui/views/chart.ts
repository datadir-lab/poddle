// bucketEvents lays the request stream onto an even time grid so it can be drawn
// as a volume line. `req` is total requests in the bin; `intervened` is the slice
// that was redacted, denied, or blocked (same unit — one y-axis, never two).
// Moved verbatim from src/web/dashboard/src/main.tsx.
import type { Event } from "./types";

export type TBucket = { t0: number; req: number; intervened: number };

export function bucketEvents(events: Event[], n = 24): TBucket[] {
  const reqs = events.filter((e) => e.kind === "request" && e.time && !Number.isNaN(new Date(e.time as string).getTime()));
  if (reqs.length < 2) return [];
  let min = Infinity, max = -Infinity;
  const ts = reqs.map((e) => { const t = new Date(e.time).getTime(); if (t < min) min = t; if (t > max) max = t; return t; });
  if (max <= min) max = min + 1;
  const width = (max - min) / n;
  const bk: TBucket[] = Array.from({ length: n }, (_, i) => ({ t0: min + i * width, req: 0, intervened: 0 }));
  reqs.forEach((e, i) => {
    let idx = Math.floor((ts[i] - min) / width);
    if (idx < 0) idx = 0; else if (idx >= n) idx = n - 1;
    bk[idx].req++;
    if (e.decision === "redact" || e.decision === "deny" || e.decision === "block") bk[idx].intervened++;
  });
  return bk;
}
