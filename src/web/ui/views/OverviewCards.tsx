import type { Stats } from "./types";
import { StatCard } from "./StatCard";

// OverviewCards renders the four headline stat cards. The container computes
// `stats` (summarise(events), with `pods` overridden by the LIVE fleet count —
// not the audit-history pod count — see the OverviewView container).
export function OverviewCards({ stats }: { stats: Stats }) {
  return (
    <div class="cards">
      <StatCard n={stats.pods} label="pods active" icon="pods" />
      <StatCard n={stats.requests} label="requests" icon="globe" />
      <StatCard n={stats.secrets} label="secrets redacted" icon="eyeoff" tone={stats.secrets ? "warn" : undefined} />
      <StatCard n={stats.blocked + stats.denied} label="blocked / denied" icon="ban" tone={stats.blocked + stats.denied ? "flag" : undefined} />
    </div>
  );
}
