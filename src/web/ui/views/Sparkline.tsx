import { threshTone } from "./aggregate";

// Sparkline is a word-sized, fixed-scale (0–100% of limit) micro-chart: a faint
// area fill for magnitude, the line banked into the cell, and a threshold-
// colored end-dot anchoring the current reading next to its number (Tufte/Few).
export function Sparkline({ data }: { data: number[] }) {
  const w = 80, h = 20, pad = 2.5;
  if (data.length < 2) return <span class="spark spark--empty faint">╌</span>;
  const last = data.length - 1;
  const clamp = (v: number) => Math.min(Math.max(v, 0), 100);
  const x = (i: number) => pad + (i / last) * (w - pad * 2);
  const y = (v: number) => h - pad - (clamp(v) / 100) * (h - pad * 2);
  const line = data.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  const cur = data[last];
  return (
    <svg class={"spark spark--" + threshTone(cur)} width={w} height={h} viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none" aria-hidden="true">
      <polygon class="spark__area" points={`${x(0).toFixed(1)},${h - pad} ${line} ${x(last).toFixed(1)},${h - pad}`} />
      <polyline class="spark__line" points={line} fill="none" />
      <circle class="spark__dot" cx={x(last).toFixed(1)} cy={y(cur).toFixed(1)} r="1.9" />
    </svg>
  );
}
