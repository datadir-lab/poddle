import type { Dest } from "./types";
import { Icon } from "./Icon";
import { MixBar } from "./MixBar";
import { SkelTable } from "./Skeletons";
import { rowKey } from "./aggregate";

// DestinationsTable renders the aggregated egress-by-host table (the
// container computes `dests` via aggregate.ts's `destinations(events)`, and
// owns the filter box + empty-state copy since those need to know whether a
// filter is active). Row click calls `onSelect(upstream)` — the container
// decides where that goes (the core dashboard drills into the audit feed).
export function DestinationsTable({ dests, loading, onSelect }: {
  dests: Dest[];
  loading: boolean;
  onSelect: (upstream: string) => void;
}) {
  if (loading) return <SkelTable rows={6} />;
  return (
    <div class="table-wrap">
      <table>
        <thead>
          <tr><th scope="col">destination</th><th scope="col" class="num">requests</th><th scope="col">decision mix</th><th scope="col" class="num">pods</th><th scope="col" class="num">secrets</th></tr>
        </thead>
        <tbody>
          {dests.map((d) => (
            <tr key={d.upstream} class="clickable" tabIndex={0} onClick={() => onSelect(d.upstream)} onKeyDown={rowKey(() => onSelect(d.upstream))}>
              <td class="c-mono dest__host">{d.upstream}{(d.deny || d.block) > 0 && <span class="dest__flag" aria-hidden="true" title="denied or blocked here"><Icon name="ban" size={12} /></span>}</td>
              <td class="num c-mono">{d.total}</td>
              <td><MixBar d={d} /></td>
              <td class="num c-mono" title={[...d.pods].join(", ")}>{d.pods.size}</td>
              <td class="num c-mono">{d.secrets || <span class="faint">—</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
