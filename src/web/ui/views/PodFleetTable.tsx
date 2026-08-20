import type { ComponentChildren } from "preact";
import type { Pod, Hist } from "./types";
import { Sparkline } from "./Sparkline";
import { cap1 } from "./aggregate";

// PodFleetTable is the pure render half of the Pods view: the live poll
// (usePods, in the core dashboard's container) stays out of this file — it only
// renders the fleet table from props. `emptyState` lets the container supply
// its own copy (the CLI dashboard says "start one with poddle up").
export function PodFleetTable({ pods, hist, onPod, emptyState }: {
  pods: Pod[];
  hist: Hist;
  onPod: (pod: string) => void;
  emptyState: ComponentChildren;
}) {
  return (
    <div class="table-wrap">
      <table>
        <thead>
          <tr><th scope="col">pod</th><th scope="col">state</th><th scope="col">size</th><th scope="col">mode</th><th scope="col">policy</th><th scope="col" class="num">cpu</th><th scope="col" class="num">memory</th></tr>
        </thead>
        <tbody>
          {pods.length === 0 && <tr><td colSpan={7} class="empty">{emptyState}</td></tr>}
          {pods.map((p) => {
            const h = hist[p.name] || { cpu: [], mem: [] };
            return (
              <tr key={p.name} class="clickable" onClick={() => onPod(p.name)}>
                <td class="c-pod">{p.name}{p.autoscale && <span class="tag">auto</span>}</td>
                <td><span class={"state state--" + p.state}>{p.state}</span></td>
                <td class="c-mono">{cap1(p.size)}</td>
                <td class="c-mono">{p.mode ? cap1(p.mode) : <span class="faint">—</span>}</td>
                <td class="c-mono">{p.policy || <span class="faint">—</span>}</td>
                <td class="perf"><Sparkline data={h.cpu} /><span class="c-mono">{p.cpu || "—"}</span></td>
                <td class="perf"><Sparkline data={h.mem} /><span class="c-mono">{p.memPerc || "—"}</span></td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
