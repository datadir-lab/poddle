import type { Pod } from "./types";
import { threshTone } from "./aggregate";

// FleetLoad: a compact per-pod CPU bar (threshold-toned, like the sparklines) so
// the running fleet's load reads at a glance without leaving the overview.
export function FleetLoad({ pods }: { pods: Pod[] }) {
  const running = pods.filter((p) => p.state === "running");
  if (running.length === 0) return <div class="chart-empty">No pods running right now.</div>;
  return (
    <div class="fleet">
      {running.map((p) => {
        const cpu = parseFloat(p.cpu) || 0;
        return (
          <div key={p.name} class="fleet__row" title={`${p.name}: CPU ${p.cpu}, memory ${p.memPerc}`}>
            <span class="fleet__name">{p.name}</span>
            <span class="fleet__track" aria-hidden="true">
              <span class={"fleet__fill fleet__fill--" + threshTone(cpu)} style={`width:${Math.min(100, cpu)}%`} />
            </span>
            <span class="fleet__val c-mono">{p.cpu || "—"}</span>
            <span class="fleet__mem c-mono" title="memory in use">{p.memPerc || "—"}</span>
          </div>
        );
      })}
    </div>
  );
}
