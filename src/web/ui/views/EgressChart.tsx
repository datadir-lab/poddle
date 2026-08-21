import { useMemo, useState } from "preact/hooks";
import type { Event } from "./types";
import { bucketEvents } from "./chart";
import { relTime } from "./aggregate";

// EgressChart: request volume over time as stacked columns — the allowed share
// (accent, anchored to the baseline) with the redacted/blocked share (amber)
// stacked on top, so each column's height is the total and its split is the
// posture. Per-column hover tooltip, per the dataviz interaction default; the
// raw rows live in the Audit tab, which is the table view.
export function EgressChart({ events }: { events: Event[] }) {
  const [hi, setHi] = useState<number | null>(null);
  const bk = useMemo(() => bucketEvents(events, 14), [events]);
  if (bk.length === 0) return <div class="chart-empty">No egress yet. Requests chart here as your agents run.</div>;

  const W = 1000, H = 172, padT = 14, padB = 22, padX = 8;
  const plotH = H - padT - padB, plotW = W - padX * 2, n = bk.length;
  const y0 = padT + plotH;
  const max = Math.max(1, ...bk.map((b) => b.req));
  const step = plotW / n;
  const barw = Math.min(46, step * 0.6);
  const cx = (i: number) => padX + (i + 0.5) * step;
  const hpx = (v: number) => (v / max) * plotH;
  const total = bk.reduce((s, b) => s + b.req, 0);
  const totalInt = bk.reduce((s, b) => s + b.intervened, 0);
  const active = hi != null ? bk[hi] : null;

  return (
    <div class="chart">
      <svg class="plot" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="xMidYMid meet" role="img"
        aria-label={`Egress over time: ${total} requests, ${totalInt} redacted or blocked, across ${n} intervals`}>
        <line class="grid grid--soft" x1={padX} y1={padT} x2={padX + plotW} y2={padT} vector-effect="non-scaling-stroke" />
        <text class="axtick" x={padX} y={padT - 4}>{max}</text>
        <line class="grid" x1={padX} y1={y0} x2={padX + plotW} y2={y0} vector-effect="non-scaling-stroke" />
        {bk.map((b, i) => {
          const allow = b.req - b.intervened;
          const aH = hpx(allow), iH = hpx(b.intervened);
          const x = cx(i) - barw / 2;
          const dim = hi != null && hi !== i ? " bar--dim" : "";
          const gap = b.intervened > 0 && allow > 0 ? 2 : 0;
          return (
            <g key={i}>
              {allow > 0 && <rect class={"bar bar--allow" + dim} x={x} y={y0 - aH} width={barw} height={aH} rx="3" />}
              {b.intervened > 0 && <rect class={"bar bar--int" + dim} x={x} y={y0 - aH - gap - iH} width={barw} height={iH} rx="3" />}
              <rect x={cx(i) - step / 2} y={padT} width={step} height={plotH} fill="transparent"
                onMouseEnter={() => setHi(i)} onMouseLeave={() => setHi(null)} />
            </g>
          );
        })}
        <text class="axlabel" x={padX} y={H - 6} text-anchor="start">{relTime(new Date(bk[0].t0).toISOString())}</text>
        <text class="axlabel" x={padX + plotW} y={H - 6} text-anchor="end">now</text>
      </svg>
      {active && (
        <div class="tip" style={`left:${(((hi! + 0.5) / n) * 100).toFixed(2)}%`} aria-hidden="true">
          <div class="tip__t">{relTime(new Date(active.t0).toISOString())} · {active.req} total</div>
          <div class="tip__row"><span class="tip__k"><span class="dotmark dotmark--req" />Allowed</span><span class="tip__v">{active.req - active.intervened}</span></div>
          <div class="tip__row"><span class="tip__k"><span class="dotmark dotmark--int" />Intervened</span><span class="tip__v">{active.intervened}</span></div>
        </div>
      )}
    </div>
  );
}
