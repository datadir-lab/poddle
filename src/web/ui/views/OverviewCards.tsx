import type { Stats } from "./types";
import { StatCard } from "./StatCard";

// OverviewCards renders the four headline stat cards. The container computes
// `stats` (summarise(events), with `pods` overridden by the LIVE fleet count —
// not the audit-history pod count — see OverviewView).
export function OverviewCards({ stats }: { stats: Stats }) {
  return (
    <div class="cards">
      <StatCard n={stats.pods} label="pods active" />
      <StatCard n={stats.requests} label="requests" />
      <StatCard n={stats.secrets} label="secrets redacted" tone={stats.secrets ? "warn" : undefined} />
      <StatCard n={stats.blocked + stats.denied} label="blocked / denied" tone={stats.blocked + stats.denied ? "flag" : undefined} />
    </div>
  );
}
