import type { Event, Pod } from "./types";
import { Sparkline } from "./Sparkline";
import { AuditLogTable } from "./AuditLogTable";
import { Fact } from "./Fact";
import { cap1 } from "./aggregate";

// PodDetailPanel is the pure render half of the pod drill-down: the container
// resolves the pod (usePods() + find(name)) and its rolling cpu/mem history;
// this component only renders. `onBack`/`onPolicyClick` are already-bound click
// handlers from the container's router (so no navigation code lives here).
export function PodDetailPanel({ name, pod, hist, events, onBack, onPolicyClick }: {
  name: string;
  pod?: Pod;
  hist: { cpu: number[]; mem: number[] };
  events: Event[];
  onBack: (e: MouseEvent) => void;
  onPolicyClick?: (e: MouseEvent) => void;
}) {
  return (
    <div>
      <div class="detail-head">
        <a href="/pods" class="back" onClick={onBack}>← Pods</a>
        <h1 class="detail-title">{name}</h1>
        {pod
          ? <span class={"state state--" + pod.state}>{pod.state}</span>
          : <span class="state state--stopped">not running</span>}
        {pod?.autoscale && <span class="tag">auto</span>}
      </div>

      {pod && (
        <dl class="facts">
          <Fact label="size"><span class="c-mono">{cap1(pod.size)}</span></Fact>
          <Fact label="mode"><span class="c-mono">{pod.mode ? cap1(pod.mode) : "—"}</span></Fact>
          <Fact label="policy">
            {pod.policy
              ? <a class="fact-link c-mono" href={`/policies/${encodeURIComponent(pod.policy)}`} onClick={onPolicyClick}>{pod.policy}</a>
              : <span class="faint">none</span>}
          </Fact>
          <Fact label="cpu"><span class="perf-inline"><Sparkline data={hist.cpu} /><span class="c-mono">{pod.cpu || "—"}</span></span></Fact>
          <Fact label="memory"><span class="perf-inline"><Sparkline data={hist.mem} /><span class="c-mono">{pod.mem || "—"}</span></span></Fact>
        </dl>
      )}

      <h2 class="section-title">Audit trail</h2>
      <AuditLogTable events={events} initialPod={name} />
    </div>
  );
}
