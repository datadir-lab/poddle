import { render } from "preact";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import "@fontsource-variable/inter";
import "@fontsource-variable/fraunces/full.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource/jetbrains-mono/600.css";
import "./style.css";

// Runtime data-source config: the SAME bundle serves local (defaults) and the
// enterprise cloud collector, which injects { apiBase, auth, multiHost }.
type Cfg = { apiBase: string; auth: string | null; multiHost?: boolean };
const CFG: Cfg = { apiBase: "/v1", auth: null, ...(window as any).__PODDLE__ };
const H: Record<string, string> = CFG.auth ? { Authorization: "Bearer " + CFG.auth } : {};

type Event = {
  seq: number; time: string; source?: string; pod?: string;
  kind: string; upstream?: string; method?: string; path?: string;
  status?: number; decision?: string; detail?: string;
};
type Policy = {
  name: string; allow_upstreams?: string[]; deny_upstreams?: string[];
  methods?: Record<string, string[]>; egress?: string;
};
type Pod = {
  name: string; state: string; size: string; mode: string; policy: string;
  autoscale: boolean; cpu: string; memPerc: string; mem: string;
};

// Platform-aware modifier hint: ⌘ on macOS, Ctrl elsewhere (the handler accepts
// either meta or ctrl regardless, so this only affects the displayed shortcut).
const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/i.test(navigator.platform || navigator.userAgent || "");
const CMD_HINT = IS_MAC ? "⌘K" : "Ctrl K";

const api = {
  audit: (limit = 1000) => fetch(`${CFG.apiBase}/audit?limit=${limit}`, { headers: H }).then((r) => r.json()),
  verify: () => fetch(`${CFG.apiBase}/audit/verify`, { headers: H }).then((r) => r.json()),
  pods: () => fetch(`${CFG.apiBase}/pods`, { headers: H }).then((r) => r.json()),
  policies: () => fetch(`${CFG.apiBase}/policies`, { headers: H }).then((r) => r.json()),
  putPolicy: (p: Policy) =>
    fetch(`${CFG.apiBase}/policies/${encodeURIComponent(p.name)}`, {
      method: "PUT", headers: { ...H, "Content-Type": "application/json" }, body: JSON.stringify(p),
    }),
  delPolicy: (name: string) =>
    fetch(`${CFG.apiBase}/policies/${encodeURIComponent(name)}`, { method: "DELETE", headers: H }),
  // Bind a policy to a live pod (the gateway enforces it on the next request).
  bindPodPolicy: (pod: string, p: Policy) =>
    fetch(`${CFG.apiBase}/pods/${encodeURIComponent(pod)}/policy`, {
      method: "POST", headers: { ...H, "Content-Type": "application/json" }, body: JSON.stringify(p),
    }),
  // Revoke every credential handle the daemon issued to a pod (a kill-switch).
  revokePod: (pod: string) =>
    fetch(`${CFG.apiBase}/pods/${encodeURIComponent(pod)}`, { method: "DELETE", headers: H }),
};

// ---- router ----
// A tiny dependency-free history router. The Go handler serves the SPA shell for
// any non-asset path, so these URLs deep-link and survive a refresh.
type Route =
  | { view: "overview" }
  | { view: "pods" }
  | { view: "pod"; name: string }
  | { view: "audit"; pod?: string; q?: string }
  | { view: "destinations" }
  | { view: "policies"; name?: string };

function parseRoute(path: string): Route {
  const [p, qs] = path.split("?");
  const seg = p.split("/").filter(Boolean);
  const query = new URLSearchParams(qs || "");
  switch (seg[0]) {
    case "pods":
      return seg[1] ? { view: "pod", name: decodeURIComponent(seg[1]) } : { view: "pods" };
    case "audit":
      return { view: "audit", pod: query.get("pod") || undefined, q: query.get("q") || undefined };
    case "destinations":
      return { view: "destinations" };
    case "policies":
      return { view: "policies", name: seg[1] ? decodeURIComponent(seg[1]) : undefined };
    default:
      return { view: "overview" };
  }
}

function useRoute(): Route {
  const [path, setPath] = useState(location.pathname + location.search);
  useEffect(() => {
    const on = () => setPath(location.pathname + location.search);
    addEventListener("popstate", on);
    return () => removeEventListener("popstate", on);
  }, []);
  return parseRoute(path);
}

function navigate(to: string) {
  if (to === location.pathname + location.search) return;
  history.pushState(null, "", to);
  dispatchEvent(new PopStateEvent("popstate")); // notify useRoute subscribers
}

// linkTo intercepts a plain-left-click on an <a> for SPA nav while keeping the
// href real (so middle-click / ⌘-click open a new tab, and the status bar shows
// the target).
const linkTo = (to: string) => (e: MouseEvent) => {
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  navigate(to);
};

// useAudit: the single live audit source (initial fetch + SSE tail), shared by
// the Overview and Audit views so there is one subscription. Exposes the initial
// loading state and the live-stream connection status.
type Conn = "connecting" | "live" | "down";
// onLive (kept in a ref so it can change without resubscribing) fires once per
// *streamed* event — never for the initial fetch — so callers can react to live
// activity (e.g. toast a new denial) without double-firing on load.
function useAudit(onLive?: (ev: Event) => void): { events: Event[]; loading: boolean; status: Conn } {
  const [events, setEvents] = useState<Event[]>([]);
  const [loading, setLoading] = useState(true);
  const [status, setStatus] = useState<Conn>("connecting");
  const liveRef = useRef(onLive);
  liveRef.current = onLive;
  useEffect(() => {
    api.audit().then((es: Event[]) => setEvents(es || [])).catch(() => {}).finally(() => setLoading(false));
    const src = new EventSource(`${CFG.apiBase}/audit/stream`);
    src.onopen = () => setStatus("live");
    src.onmessage = (e) => {
      try { const ev = JSON.parse(e.data); setEvents((prev) => [ev, ...prev].slice(0, 4000)); liveRef.current?.(ev); } catch {}
    };
    src.onerror = () => setStatus("down");
    return () => src.close();
  }, []);
  return { events, loading, status };
}

type Verify = { ok: boolean; brokenAt: number } | null;
// useVerify polls the hash-chain check, tracks when it last ran, and exposes a
// manual re-verify (bumping `nonce` re-runs the effect for an immediate check).
function useVerify(): { verify: Verify; checkedAt: number; recheck: () => void } {
  const [verify, setVerify] = useState<Verify>(null);
  const [checkedAt, setCheckedAt] = useState(0);
  const [nonce, setNonce] = useState(0);
  useEffect(() => {
    let alive = true;
    const tick = () => api.verify()
      .then((r) => { if (alive) { setVerify(r); setCheckedAt(Date.now()); } })
      .catch(() => { if (alive) setVerify(null); });
    tick();
    const id = setInterval(tick, 15000);
    return () => { alive = false; clearInterval(id); };
  }, [nonce]);
  return { verify, checkedAt, recheck: () => setNonce((n) => n + 1) };
}

// usePods polls /v1/pods and keeps a rolling CPU/mem history per pod for the
// sparklines (the browser is the time-series store — no server needed).
type Hist = Record<string, { cpu: number[]; mem: number[] }>;
function usePods(): { pods: Pod[]; hist: Hist; loading: boolean } {
  const [pods, setPods] = useState<Pod[]>([]);
  const [hist, setHist] = useState<Hist>({});
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    const tick = () => api.pods().then((ps: Pod[]) => {
      setPods(ps || []);
      setHist((h) => {
        const nh: Hist = { ...h };
        for (const p of ps || []) {
          const cur = nh[p.name] || { cpu: [], mem: [] };
          nh[p.name] = {
            cpu: [...cur.cpu, parseFloat(p.cpu) || 0].slice(-40),
            mem: [...cur.mem, parseFloat(p.memPerc) || 0].slice(-40),
          };
        }
        return nh;
      });
    }).catch(() => {}).finally(() => setLoading(false));
    tick();
    const id = setInterval(tick, 3000);
    return () => clearInterval(id);
  }, []);
  return { pods, hist, loading };
}

// threshTone maps a live % (of the pod's limit) to a severity tone so the
// sparkline carries state, not just shape (Grafana's threshold-colored cells).
const threshTone = (v: number) => (v >= 85 ? "hot" : v >= 60 ? "warm" : "cool");

// Spark is a word-sized, fixed-scale (0–100% of limit) micro-chart: a faint
// area fill for magnitude, the line banked into the cell, and a threshold-
// colored end-dot anchoring the current reading next to its number (Tufte/Few).
function Spark({ data }: { data: number[] }) {
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

// ---- icons ----
// A small inline-SVG set (lucide-style, matching the theme-toggle glyphs) so the
// nav, stat cards, and chart legends carry meaning by shape as well as label -
// and it keeps the bundle dependency-free for go:embed. Each entry is a render
// fn (not a shared vnode) so the same icon can be drawn in many places safely.
const ICONS: Record<string, () => any> = {
  overview: () => (<><rect x="3" y="3" width="7" height="7" rx="1.4" /><rect x="14" y="3" width="7" height="7" rx="1.4" /><rect x="14" y="14" width="7" height="7" rx="1.4" /><rect x="3" y="14" width="7" height="7" rx="1.4" /></>),
  pods: () => (<><path d="M21 8v8a2 2 0 0 1-1 1.73l-7 4a2 2 0 0 1-2 0l-7-4A2 2 0 0 1 3 16V8a2 2 0 0 1 1-1.73l7-4a2 2 0 0 1 2 0l7 4A2 2 0 0 1 21 8Z" /><path d="m3.3 7 8.7 5 8.7-5" /><path d="M12 22V12" /></>),
  audit: () => (<path d="M22 12h-4l-3 9L9 3l-3 9H2" />),
  policies: () => (<><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67 0C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1Z" /><path d="m9 12 2 2 4-4" /></>),
  globe: () => (<><circle cx="12" cy="12" r="10" /><path d="M12 2a15 15 0 0 1 0 20 15 15 0 0 1 0-20M2 12h20" /></>),
  eyeoff: () => (<><path d="M9.88 9.88a3 3 0 1 0 4.24 4.24" /><path d="M10.73 5.08A11 11 0 0 1 12 5c7 0 10 7 10 7a13 13 0 0 1-1.67 2.68" /><path d="M6.61 6.61A13 13 0 0 0 2 12s3 7 10 7a11 11 0 0 0 5.39-1.39" /><line x1="2" y1="2" x2="22" y2="22" /></>),
  ban: () => (<><circle cx="12" cy="12" r="10" /><path d="m4.9 4.9 14.2 14.2" /></>),
  check: () => (<path d="M20 6 9 17l-5-5" />),
  octagon: () => (<><polygon points="7.86 2 16.14 2 22 7.86 22 16.14 16.14 22 7.86 22 2 16.14 2 7.86" /><line x1="15" y1="9" x2="9" y2="15" /><line x1="9" y1="9" x2="15" y2="15" /></>),
  panel: () => (<><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="9" y1="3" x2="9" y2="21" /></>),
  search: () => (<><circle cx="11" cy="11" r="7" /><line x1="21" y1="21" x2="16.65" y2="16.65" /></>),
  theme: () => (<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" />),
};
function Icon({ name, size = 16 }: { name: string; size?: number }) {
  const draw = ICONS[name];
  if (!draw) return null;
  return (
    <svg class="icon" width={size} height={size} viewBox="0 0 24 24" fill="none"
      stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      {draw()}
    </svg>
  );
}

// PoddleMark is the real product mark (the isometric pod cube from the site
// favicon). The two side faces ride on currentColor (= --ink) so the logo reads
// on both the cream and the near-black rails; the top face and the prompt glyph
// keep the fixed brand green.
function PoddleMark({ size = 30 }: { size?: number }) {
  return (
    <svg class="pmark" width={size} height={size} viewBox="382.0 134.1 435.9 435.9" aria-hidden="true">
      <path d="M769.71,450.00 L769.71,254.04 L600.00,352.02 L600.00,547.98 Z" fill="currentColor" />
      <path d="M600.00,547.98 L600.00,352.02 L430.29,254.04 L430.29,450.00 Z" fill="currentColor" />
      <path d="M769.71,254.04 L600.00,156.06 L430.29,254.04 L600.00,352.02 Z" fill="#2f9e6f" />
      <g transform="matrix(169.7056,97.9787,0.0000,195.9601,430.29,254.04)" fill="#2f9e6f">
        <path d="M0.19,0.31 L0.29,0.31 L0.44,0.50 L0.29,0.69 L0.19,0.69 L0.34,0.50 Z" />
        <path d="M0.50,0.605 L0.72,0.605 L0.72,0.685 L0.50,0.685 Z" />
      </g>
    </svg>
  );
}

// The four egress decisions, in fixed order, each with its status glyph. These
// are *status* colours (reserved, in tokens.css) so they always ship with a
// label + icon, never colour alone.
const DECISIONS = [
  { key: "allow", label: "Allow", icon: "check" },
  { key: "redact", label: "Redact", icon: "eyeoff" },
  { key: "deny", label: "Deny", icon: "ban" },
  { key: "block", label: "Block", icon: "octagon" },
] as const;

// ---- charts (hand-rolled SVG/HTML, no chart lib — keeps the go:embed bundle small) ----
function decisionCounts(events: Event[]): Record<string, number> {
  const c: Record<string, number> = { allow: 0, redact: 0, deny: 0, block: 0 };
  for (const e of events) if (e.decision && e.decision in c) c[e.decision]++;
  return c;
}

// bucketEvents lays the request stream onto an even time grid so it can be drawn
// as a volume line. `req` is total requests in the bin; `intervened` is the slice
// that was redacted, denied, or blocked (same unit — one y-axis, never two).
type TBucket = { t0: number; req: number; intervened: number };
function bucketEvents(events: Event[], n = 24): TBucket[] {
  const reqs = events.filter((e) => e.kind === "request" && e.time);
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

// EgressChart: request volume over time as stacked columns — the allowed share
// (accent, anchored to the baseline) with the redacted/blocked share (amber)
// stacked on top, so each column's height is the total and its split is the
// posture. Per-column hover tooltip, per the dataviz interaction default; the
// raw rows live in the Audit tab, which is the table view.
function EgressChart({ events }: { events: Event[] }) {
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

// PostureBar: the decision mix as a single proportional bar + a labelled legend.
// Segments carry a status colour, a glyph, and a count — identity never rests on
// colour alone (deny and block share the red, told apart by their icons/labels).
function PostureBar({ counts }: { counts: Record<string, number> }) {
  const total = DECISIONS.reduce((s, d) => s + (counts[d.key] || 0), 0);
  if (total === 0) return <div class="chart-empty">No decisions recorded yet.</div>;
  const pct = (v: number) => Math.round((v / total) * 100);
  return (
    <div class="posture">
      <div class="posture__bar" role="img"
        aria-label={"Decision mix: " + DECISIONS.map((d) => `${counts[d.key] || 0} ${d.label}`).join(", ")}>
        {DECISIONS.filter((d) => (counts[d.key] || 0) > 0).map((d) => (
          <div key={d.key} class={"posture__seg d-" + d.key} style={`flex-grow:${counts[d.key]}`}
            title={`${d.label}: ${counts[d.key]} (${pct(counts[d.key])}%)`} />
        ))}
      </div>
      <ul class="legend">
        {DECISIONS.map((d) => (
          <li key={d.key} class="legend__i">
            <span class={"legend__mk d-" + d.key}><Icon name={d.icon} size={13} /></span>
            <span class="legend__lb">{d.label}</span>
            <span class="legend__v">{counts[d.key] || 0}</span>
            <span class="legend__pc">{pct(counts[d.key] || 0)}%</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

// FleetLoad: a compact per-pod CPU bar (threshold-toned, like the sparklines) so
// the running fleet's load reads at a glance without leaving the overview.
function FleetLoad({ pods }: { pods: Pod[] }) {
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

// ---- aggregations (derived client-side from the audit events) ----
const secretsFrom = (detail?: string) => { const m = (detail || "").match(/redacted (\d+)/); return m ? +m[1] : 1; };

function summarise(events: Event[]) {
  const pods = new Set<string>();
  let requests = 0, redactions = 0, secrets = 0, blocked = 0, denied = 0;
  for (const e of events) {
    if (e.pod) pods.add(e.pod);
    if (e.kind === "request") requests++;
    if (e.decision === "redact") { redactions++; secrets += secretsFrom(e.detail); }
    if (e.decision === "block") blocked++;
    if (e.decision === "deny") denied++;
  }
  return { pods: pods.size, requests, redactions, secrets, blocked, denied };
}

type Grouped = { pod: string; decision: string; upstream: string; count: number; secrets: number };
function group(events: Event[], decisions: string[]): Grouped[] {
  const m = new Map<string, Grouped>();
  for (const e of events) {
    if (!e.decision || !decisions.includes(e.decision)) continue;
    const key = `${e.pod || "—"}|${e.decision}|${e.upstream || "—"}`;
    const g = m.get(key) || { pod: e.pod || "—", decision: e.decision, upstream: e.upstream || "—", count: 0, secrets: 0 };
    g.count++;
    if (e.decision === "redact") g.secrets += secretsFrom(e.detail);
    m.set(key, g);
  }
  return [...m.values()].sort((a, b) => b.count - a.count);
}

// cap1 upper-cases only the first letter (leaves identifiers/values intact).
const cap1 = (s: string) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);
// humanKind turns a dotted event kind into a readable label: "pod.up" -> "Pod up".
const humanKind = (k: string) => cap1((k || "").replace(/\./g, " "));

// relTime renders an event's age compactly (the absolute time goes in a tooltip).
function relTime(iso: string): string {
  const s = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 5) return "just now";
  if (s < 60) return s + "s ago";
  const m = Math.floor(s / 60);
  if (m < 60) return m + "m ago";
  const h = Math.floor(m / 60);
  if (h < 24) return h + "h ago";
  return Math.floor(h / 24) + "d ago";
}

// ---- views ----
// VerifyBadge is the at-a-glance provenance signal. It links to the Audit view,
// where the integrity panel explains the hash-chain in full; the tooltip gives
// the short version on hover.
function VerifyBadge({ v, compact }: { v: Verify; compact?: boolean }) {
  const cls = v == null ? "badge" : v.ok ? "badge ok" : "badge bad";
  const label = v == null ? "Verifying…" : v.ok ? "Chain intact ✓" : `Chain broken @${v.brokenAt} ✗`;
  const tip = v == null
    ? "Checking the audit hash-chain…"
    : v.ok
      ? "Every audit event is hash-linked to the one before it, so any edit or deletion is detectable. Intact means nothing was tampered with. Click to open the audit trail."
      : `The audit hash-chain is broken at event #${v.brokenAt}: a row was altered or removed. Click to open the audit trail.`;
  // aria-label pins the accessible name (the CSS tooltip's ::after text would
  // otherwise fold into it); the visual tooltip carries the fuller explanation.
  if (compact) {
    return (
      <a class={cls + " badge--icon"} href="/audit" title={label} aria-label={label} onClick={linkTo("/audit")}>
        <Icon name={v && !v.ok ? "octagon" : "policies"} size={15} />
      </a>
    );
  }
  return <a class={cls} href="/audit" data-tip={tip} aria-label={label} onClick={linkTo("/audit")}>{label}</a>;
}

// IntegrityPanel is the provenance centerpiece of the Audit view: it states the
// hash-chain verdict in plain language, shows when it was last checked, and lets
// the operator re-verify on demand. A broken chain names the first bad seq.
function IntegrityPanel({ verify, checkedAt, recheck, count }: { verify: Verify; checkedAt: number; recheck: () => void; count: number }) {
  const state = verify == null ? "verifying" : verify.ok ? "intact" : "broken";
  const headline = state === "verifying" ? "Verifying chain…"
    : state === "intact" ? "Audit chain intact"
      : `Chain broken at #${verify!.brokenAt}`;
  return (
    <div class={"integrity integrity--" + state}>
      <span class="integrity__icon" aria-hidden="true"><Icon name={state === "broken" ? "octagon" : "policies"} size={22} /></span>
      <div class="integrity__body">
        <div class="integrity__status">{headline}</div>
        <p class="integrity__desc">
          {state === "broken"
            ? "An event was altered or removed after it was written — everything from that point on is suspect."
            : "Every event is hash-linked to the one before it, so any edit or deletion is detectable after the fact."}
        </p>
      </div>
      <dl class="integrity__meta">
        <div><dt>Events</dt><dd>{count}</dd></div>
        <div><dt>Last verified</dt><dd>{checkedAt ? relTime(new Date(checkedAt).toISOString()) : "…"}</dd></div>
      </dl>
      <button type="button" class="btn btn--ghost btn--sm integrity__btn" onClick={recheck}>Re-verify</button>
    </div>
  );
}

function Card({ n, label, icon, tone }: { n: number | string; label: string; icon?: string; tone?: string }) {
  return (
    <div class={"card" + (tone ? " card--" + tone : "")}>
      {icon && <span class="card__icon" aria-hidden="true"><Icon name={icon} size={17} /></span>}
      <div class="card__num">{n}</div>
      <div class="card__label">{label}</div>
    </div>
  );
}

// ---- loading & live-status building blocks ----
// Skeletons fill the brief gap before the first fetch resolves, so a populated
// account never flashes its empty state on load.
function SkelCards() {
  return (
    <div class="cards" aria-hidden="true">
      {[0, 1, 2, 3].map((i) => (
        <div class="card" key={i}><span class="skel skel--num" /><span class="skel skel--sm" /></div>
      ))}
    </div>
  );
}
function SkelTable({ rows = 6 }: { rows?: number }) {
  return (
    <div class="table-wrap skel-table" aria-hidden="true" aria-busy="true">
      {Array.from({ length: rows }).map((_, i) => <div class="skel-tr" key={i}><span class="skel" /></div>)}
    </div>
  );
}
// LiveDot reflects the audit stream's connection: live, reconnecting, or connecting.
function LiveDot({ status }: { status: Conn }) {
  const txt = status === "live" ? "Live" : status === "down" ? "Reconnecting" : "Connecting";
  return (
    <span class={"live live--" + status} title={"Audit stream: " + txt} role="status">
      <span class="live__dot" aria-hidden="true" />{txt}
    </span>
  );
}

// CSV export of the (already filtered) audit rows - the "provable, exportable" story.
function toCsv(rows: Event[]): string {
  const cols: (keyof Event)[] = ["seq", "time", "pod", "kind", "decision", "upstream", "method", "status", "detail"];
  const esc = (v: unknown) => { const s = String(v ?? ""); return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s; };
  const lines = rows.map((e) => cols.map((c) => esc(e[c])).join(","));
  return [cols.join(","), ...lines].join("\n");
}
function downloadCsv(rows: Event[]) {
  const blob = new Blob([toCsv(rows)], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "poddle-audit.csv";
  document.body.appendChild(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

// rowKeys makes a table row keyboard-operable (Enter/Space) when it is clickable.
function rowKey(onClick: () => void) {
  return (e: KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onClick(); }
  };
}

// Segmented is an accessible single-select control (role=radiogroup) for a
// small set of mutually exclusive options that should all stay visible with
// immediate effect — the right pattern for egress mode and the audit filter,
// and it keeps the bundle dependency-free for go:embed. An option's `tone`
// colors the active segment by its meaning (e.g. block = deny-red).
type SegOption = { value: string; label: string; tone?: string; badge?: string | number };
function Segmented({ value, options, onChange, ariaLabel }: {
  value: string; options: SegOption[]; onChange: (v: string) => void; ariaLabel: string;
}) {
  const idx = Math.max(0, options.findIndex((o) => o.value === value));
  const onKey = (e: KeyboardEvent) => {
    let ni = idx;
    if (e.key === "ArrowRight" || e.key === "ArrowDown") ni = (idx + 1) % options.length;
    else if (e.key === "ArrowLeft" || e.key === "ArrowUp") ni = (idx - 1 + options.length) % options.length;
    else return;
    e.preventDefault();
    onChange(options[ni].value);
  };
  return (
    <div class="seg" role="radiogroup" aria-label={ariaLabel} onKeyDown={onKey}>
      {options.map((o, i) => (
        <button type="button" role="radio" aria-checked={value === o.value} data-tone={o.tone}
          tabIndex={i === idx ? 0 : -1}
          class={"seg__opt" + (value === o.value ? " on" : "")}
          onClick={() => onChange(o.value)}>
          {o.label}{o.badge != null && <span class="seg__badge" aria-hidden="true">{o.badge}</span>}
        </button>
      ))}
    </div>
  );
}

const DECISION_FILTER: SegOption[] = [
  { value: "", label: "All" },
  { value: "allow", label: "Allow", tone: "allow" },
  { value: "redact", label: "Redact", tone: "redact" },
  { value: "block", label: "Block", tone: "deny" },
  { value: "deny", label: "Deny", tone: "deny" },
];
const EGRESS_MODES: SegOption[] = [
  { value: "redact", label: "Redact", tone: "redact" },
  { value: "block", label: "Block", tone: "deny" },
  { value: "off", label: "Off", tone: "faint" },
];

function OverviewView({ events, loading, onPod }: { events: Event[]; loading: boolean; onPod: (pod: string) => void }) {
  const { pods: livePods, loading: podsLoading } = usePods(); // live fleet, not audit history
  const [range, setRange] = useState("");
  // The egress-derived cards, charts, and panels reflect the selected window; the
  // live pod count stays "now".
  const win = useMemo(() => {
    const cutoff = range && RANGE_MS[range] ? Date.now() - RANGE_MS[range] : 0;
    return cutoff ? events.filter((e) => e.time && new Date(e.time).getTime() >= cutoff) : events;
  }, [events, range]);
  const s = useMemo(() => summarise(win), [win]);
  const counts = useMemo(() => decisionCounts(win), [win]);
  const attention = useMemo(() => group(win, ["deny", "block"]).slice(0, 8), [win]);
  const redactions = useMemo(() => group(win, ["redact"]).slice(0, 12), [win]);

  if (loading && podsLoading) {
    return (
      <div>
        <SkelCards />
        <h2 class="section-title">Attention</h2>
        <SkelTable rows={3} />
        <h2 class="section-title">Secrets redacted</h2>
        <SkelTable rows={4} />
      </div>
    );
  }

  return (
    <div>
      <div class="ov-head">
        <span class="ov-head__label">Egress window</span>
        <Segmented value={range} options={TIME_RANGES} onChange={setRange} ariaLabel="overview time range" />
      </div>
      <div class="cards">
        <Card n={livePods.length} label="pods active" icon="pods" />
        <Card n={s.requests} label="requests" icon="globe" />
        <Card n={s.secrets} label="secrets redacted" icon="eyeoff" tone={s.secrets ? "warn" : undefined} />
        <Card n={s.blocked + s.denied} label="blocked / denied" icon="ban" tone={s.blocked + s.denied ? "flag" : undefined} />
      </div>

      <div class="chart-card">
        <div class="chart-head">
          <h2 class="chart-title">Egress over time</h2>
          <p class="chart-sub">Requests per interval, split by how the broker handled them.</p>
          <ul class="legend legend--inline">
            <li class="legend__i"><span class="legend__mk mk--req" /><span class="legend__lb">Allowed</span></li>
            <li class="legend__i"><span class="legend__mk mk--int" /><span class="legend__lb">Intervened</span></li>
          </ul>
        </div>
        <EgressChart events={win} />
      </div>

      <div class="grid-2">
        <div class="chart-card">
          <div class="chart-head">
            <h2 class="chart-title">Decision mix</h2>
            <p class="chart-sub">How the broker handled every request.</p>
          </div>
          <PostureBar counts={counts} />
        </div>
        <div class="chart-card">
          <div class="chart-head">
            <h2 class="chart-title">Fleet load</h2>
            <p class="chart-sub">Live CPU across running pods.</p>
          </div>
          <FleetLoad pods={livePods} />
        </div>
      </div>

      <h2 class="section-title">Attention</h2>
      {attention.length === 0
        ? <div class="panel empty">No policy denials or blocks — agents are inside their guardrails.</div>
        : <div class="panel">
            {attention.map((a) => (
              <button class="attn" onClick={() => onPod(a.pod)}>
                <span class="attn__pod">{a.pod}</span>
                <span class="attn__desc">
                  <span class={"decision d-" + a.decision}>{a.decision}</span> {a.upstream}
                </span>
                <span class="attn__count">×{a.count}</span>
              </button>
            ))}
          </div>}

      <h2 class="section-title">Secrets redacted</h2>
      {redactions.length === 0
        ? <div class="panel empty">No secrets redacted yet — redact-mode policies strip credentials the agent tries to send.</div>
        : <div class="table-wrap">
            <table>
              <thead><tr><th>pod</th><th>destination</th><th>secrets</th><th>times</th></tr></thead>
              <tbody>
                {redactions.map((c) => (
                  <tr class="clickable" tabIndex={0} onClick={() => onPod(c.pod)} onKeyDown={rowKey(() => onPod(c.pod))}>
                    <td class="c-pod">{c.pod}</td>
                    <td class="c-mono">{c.upstream}</td>
                    <td class="c-mono">{c.secrets}</td>
                    <td class="c-mono">×{c.count}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>}
    </div>
  );
}

function PodsView({ onPod }: { onPod: (pod: string) => void }) {
  const { pods, hist, loading } = usePods();
  if (loading) return <SkelTable rows={5} />;
  return (
    <div class="table-wrap">
      <table>
        <thead>
          <tr><th scope="col">pod</th><th scope="col">state</th><th scope="col">size</th><th scope="col">mode</th><th scope="col">policy</th><th scope="col" class="num">cpu</th><th scope="col" class="num">memory</th></tr>
        </thead>
        <tbody>
          {pods.length === 0 && <tr><td colSpan={7} class="empty">No pods running yet — start one with <code>poddle up</code>.</td></tr>}
          {pods.map((p) => {
            const h = hist[p.name] || { cpu: [], mem: [] };
            return (
              <tr key={p.name} class="clickable" tabIndex={0} onClick={() => onPod(p.name)} onKeyDown={rowKey(() => onPod(p.name))}>
                <td class="c-pod">{p.name}{p.autoscale && <span class="tag">auto</span>}</td>
                <td><span class={"state state--" + p.state}>{p.state}</span></td>
                <td class="c-mono">{cap1(p.size)}</td>
                <td class="c-mono">{p.mode ? cap1(p.mode) : <span class="faint">—</span>}</td>
                <td class="c-mono">{p.policy || <span class="faint">—</span>}</td>
                <td class="perf"><Spark data={h.cpu} /><span class="c-mono">{p.cpu || "—"}</span></td>
                <td class="perf"><Spark data={h.mem} /><span class="c-mono">{p.memPerc || "—"}</span></td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

const TIME_RANGES: SegOption[] = [
  { value: "", label: "All" },
  { value: "15m", label: "15m" },
  { value: "1h", label: "1h" },
  { value: "24h", label: "24h" },
];
const RANGE_MS: Record<string, number> = { "15m": 900000, "1h": 3600000, "24h": 86400000 };

function AuditView({ events, initialPod, initialQ, loading }: { events: Event[]; initialPod?: string; initialQ?: string; loading: boolean }) {
  const [q, setQ] = useState(initialPod || initialQ || "");
  const [decision, setDecision] = useState("");
  const [range, setRange] = useState("");
  useEffect(() => { if (initialPod) setQ(initialPod); else if (initialQ) setQ(initialQ); }, [initialPod, initialQ]);

  // Narrow by search + time range first; the decision filter is applied last so
  // the per-decision counts reflect everything else the user has narrowed to.
  const matched = useMemo(() => {
    const cutoff = range && RANGE_MS[range] ? Date.now() - RANGE_MS[range] : 0;
    const s = q.toLowerCase();
    return events.filter((e) => {
      if (cutoff && new Date(e.time).getTime() < cutoff) return false;
      if (!q) return true;
      return (e.pod || "").toLowerCase().includes(s) || (e.kind || "").toLowerCase().includes(s) ||
        (e.upstream || "").toLowerCase().includes(s);
    });
  }, [events, q, range]);

  const counts = useMemo(() => {
    const c: Record<string, number> = { "": matched.length, allow: 0, redact: 0, block: 0, deny: 0 };
    for (const e of matched) if (e.decision && e.decision in c) c[e.decision]++;
    return c;
  }, [matched]);
  const shown = useMemo(() => (decision ? matched.filter((e) => e.decision === decision) : matched), [matched, decision]);
  const decisionOpts = DECISION_FILTER.map((o) => ({ ...o, badge: counts[o.value] ?? 0 }));

  const toolbar = (
    <div class="toolbar">
      <input class="grow" aria-label="Filter events by pod, kind, or upstream" placeholder="Filter by pod, kind, or upstream…" value={q}
        onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
      <Segmented value={range} options={TIME_RANGES} onChange={setRange} ariaLabel="time range" />
      <Segmented value={decision} options={decisionOpts} onChange={setDecision} ariaLabel="filter by decision" />
      <button type="button" class="btn btn--ghost btn--sm" disabled={!shown.length} onClick={() => downloadCsv(shown)}>Export CSV</button>
      <span class="count">{shown.length} events</span>
    </div>
  );

  if (loading) return <div>{toolbar}<SkelTable rows={8} /></div>;

  return (
    <div>
      {toolbar}
      <div class="table-wrap">
        <table class="dense">
          <thead>
            <tr><th scope="col">time</th><th scope="col">pod</th><th scope="col">kind</th><th scope="col">decision</th><th scope="col">upstream</th><th scope="col">detail</th></tr>
          </thead>
          <tbody>
            {shown.length === 0 && (
              <tr><td colSpan={6} class="empty">
                {q || decision || range ? "No events match your filter." : "Monitoring active — no events recorded yet."}
              </td></tr>
            )}
            {shown.slice(0, 800).map((e) => (
              <tr key={e.seq} class="auditrow">
                <td class="c-time" title={new Date(e.time).toLocaleString()}>{relTime(e.time)}</td>
                <td class="c-pod">{e.pod || <span class="faint">—</span>}</td>
                <td>{humanKind(e.kind)}</td>
                <td><span class={"decision d-" + (e.decision || "")}>{e.decision || <span class="faint">—</span>}</span></td>
                <td class="c-mono">{e.upstream || <span class="faint">—</span>}</td>
                <td class="c-detail">{cap1(e.detail || "")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// ---- destinations (where the agents are reaching, derived from the audit) ----
type Dest = { upstream: string; total: number; allow: number; redact: number; deny: number; block: number; secrets: number; pods: Set<string> };
function destinations(events: Event[]): Dest[] {
  const m = new Map<string, Dest>();
  for (const e of events) {
    if (e.kind !== "request" || !e.upstream) continue;
    const d = m.get(e.upstream) || { upstream: e.upstream, total: 0, allow: 0, redact: 0, deny: 0, block: 0, secrets: 0, pods: new Set<string>() };
    d.total++;
    if (e.pod) d.pods.add(e.pod);
    switch (e.decision) {
      case "allow": d.allow++; break;
      case "redact": d.redact++; d.secrets += secretsFrom(e.detail); break;
      case "deny": d.deny++; break;
      case "block": d.block++; break;
    }
    m.set(e.upstream, d);
  }
  return [...m.values()].sort((a, b) => b.total - a.total);
}

// MixBar draws a destination's decision split as a compact proportional bar.
function MixBar({ d }: { d: Dest }) {
  const segs = ([["allow", d.allow], ["redact", d.redact], ["deny", d.deny], ["block", d.block]] as const).filter(([, n]) => n > 0);
  return (
    <span class="mix" role="img" aria-label={segs.map(([k, n]) => `${n} ${k}`).join(", ")}>
      {segs.map(([k, n]) => <span key={k} class={"mix__seg d-" + k} style={`flex-grow:${n}`} title={`${k}: ${n}`} />)}
    </span>
  );
}

const goAuditFor = (upstream: string) => navigate("/audit?q=" + encodeURIComponent(upstream));

function DestinationsView({ events, loading }: { events: Event[]; loading: boolean }) {
  const [q, setQ] = useState("");
  const all = useMemo(() => destinations(events), [events]);
  const s = q.toLowerCase();
  const shown = useMemo(() => (q ? all.filter((d) => d.upstream.toLowerCase().includes(s)) : all), [all, q, s]);
  const podCount = useMemo(() => { const p = new Set<string>(); all.forEach((d) => d.pods.forEach((x) => p.add(x))); return p.size; }, [all]);

  const toolbar = (
    <div class="toolbar">
      <input class="grow" aria-label="Filter destinations" placeholder="Filter destinations…" value={q}
        onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
      <span class="count">{all.length} destination{all.length === 1 ? "" : "s"} · {podCount} pod{podCount === 1 ? "" : "s"}</span>
    </div>
  );
  if (loading) return <div>{toolbar}<SkelTable rows={6} /></div>;

  return (
    <div>
      {toolbar}
      {shown.length === 0
        ? <div class="panel empty">{q ? "No destinations match your filter." : "No egress recorded yet — destinations appear as your agents make requests."}</div>
        : <div class="table-wrap">
            <table>
              <thead>
                <tr><th scope="col">destination</th><th scope="col" class="num">requests</th><th scope="col">decision mix</th><th scope="col" class="num">pods</th><th scope="col" class="num">secrets</th></tr>
              </thead>
              <tbody>
                {shown.map((d) => (
                  <tr key={d.upstream} class="clickable" tabIndex={0} onClick={() => goAuditFor(d.upstream)} onKeyDown={rowKey(() => goAuditFor(d.upstream))}>
                    <td class="c-mono dest__host">{d.upstream}{(d.deny || d.block) > 0 && <span class="dest__flag" aria-hidden="true" title="denied or blocked here"><Icon name="ban" size={12} /></span>}</td>
                    <td class="num c-mono">{d.total}</td>
                    <td><MixBar d={d} /></td>
                    <td class="num c-mono" title={[...d.pods].join(", ")}>{d.pods.size}</td>
                    <td class="num c-mono">{d.secrets || <span class="faint">—</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>}
    </div>
  );
}

const lines = (a?: string[]) => (a || []).join("\n");
const parseLines = (s: string) => s.split("\n").map((x) => x.trim()).filter(Boolean);

// ---- client-side policy evaluation ----
// A faithful port of policy.Decide (Go): the deny-list wins, then the allow-list
// (default-deny when non-empty), then per-host method rules; otherwise allow. A
// ".suffix" pattern matches that domain and any subdomain. Keeping this in lock-
// step with the daemon is what makes the dry-run trustworthy.
function matchHost(host: string, patterns: string[]): boolean {
  for (const p of patterns) {
    if (p === host) return true;
    if (p.startsWith(".") && (host.endsWith(p) || host === p.slice(1))) return true;
  }
  return false;
}
function methodsFor(methods: Record<string, string[]> | undefined, host: string): string[] | null {
  if (!methods) return null;
  if (host in methods) return methods[host];
  for (const k in methods) {
    if (k.startsWith(".") && (host.endsWith(k) || host === k.slice(1))) return methods[k];
  }
  return null;
}
function decide(pol: Policy, host: string, method: string): { allow: boolean; reason: string } {
  if (matchHost(host, pol.deny_upstreams || [])) return { allow: false, reason: "on the deny-list" };
  if ((pol.allow_upstreams || []).length > 0 && !matchHost(host, pol.allow_upstreams || []))
    return { allow: false, reason: "not allow-listed" };
  const allowed = methodsFor(pol.methods, host);
  if (allowed && method && method !== "CONNECT" && !allowed.some((m) => m.toUpperCase() === method.toUpperCase()))
    return { allow: false, reason: method + " not allowed here" };
  return { allow: true, reason: "" };
}

// dryRun replays a (possibly unsaved) policy over the recent request stream and
// reports what its allow/deny rules would decide. Secret redaction depends on
// request payloads, so it is deliberately out of scope — this is access control.
type DryRow = { upstream: string; method: string; reason: string; count: number };
function dryRun(pol: Policy, events: Event[]): { total: number; denied: number; rows: DryRow[] } {
  const reqs = events.filter((e) => e.kind === "request" && e.upstream);
  const m = new Map<string, DryRow>();
  let denied = 0;
  for (const e of reqs) {
    const d = decide(pol, e.upstream as string, e.method || "");
    if (d.allow) continue;
    denied++;
    const key = `${e.method || ""}|${e.upstream}`;
    const row = m.get(key) || { upstream: e.upstream as string, method: e.method || "", reason: d.reason, count: 0 };
    row.count++;
    m.set(key, row);
  }
  return { total: reqs.length, denied, rows: [...m.values()].sort((a, b) => b.count - a.count) };
}

function PolicyEditor({ policy, events, onSaved, onDeleted }: { policy: Policy; events: Event[]; onSaved: (name: string) => void; onDeleted: () => void }) {
  const [name, setName] = useState(policy.name);
  const [allow, setAllow] = useState(lines(policy.allow_upstreams));
  const [deny, setDeny] = useState(lines(policy.deny_upstreams));
  const [egress, setEgress] = useState(policy.egress || "redact");
  const [methods, setMethods] = useState(JSON.stringify(policy.methods || {}, null, 2));
  const [err, setErr] = useState("");

  // Live dry-run: re-evaluate the (unsaved) form against recent traffic on every
  // keystroke, so the impact of an allow/deny edit is visible before saving.
  const impact = useMemo(() => {
    let m: Record<string, string[]> = {};
    try { m = methods.trim() ? JSON.parse(methods) : {}; } catch { m = {}; }
    const draft: Policy = { name: name.trim(), allow_upstreams: parseLines(allow), deny_upstreams: parseLines(deny), methods: m, egress };
    return dryRun(draft, events);
  }, [name, allow, deny, methods, egress, events]);

  useEffect(() => {
    setName(policy.name); setAllow(lines(policy.allow_upstreams)); setDeny(lines(policy.deny_upstreams));
    setEgress(policy.egress || "redact"); setMethods(JSON.stringify(policy.methods || {}, null, 2)); setErr("");
  }, [policy]);

  const save = async () => {
    let parsedMethods: Record<string, string[]> = {};
    try { parsedMethods = methods.trim() ? JSON.parse(methods) : {}; }
    catch { setErr("methods must be valid JSON, e.g. {\"git.internal\":[\"GET\"]}"); return; }
    if (!name.trim()) { setErr("name is required"); return; }
    const res = await api.putPolicy({
      name: name.trim(), allow_upstreams: parseLines(allow), deny_upstreams: parseLines(deny),
      methods: parsedMethods, egress,
    });
    if (!res.ok) { setErr("save failed: " + res.status); return; }
    onSaved(name.trim());
  };
  const del = async () => { await api.delPolicy(policy.name); onDeleted(); };

  return (
    <div class="editor">
      <div class="row">
        <div>
          <label for="pol-name">Name</label>
          <input id="pol-name" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        </div>
        <div class="narrow">
          <label>Egress mode</label>
          <Segmented value={egress} options={EGRESS_MODES} onChange={setEgress} ariaLabel="egress mode" />
        </div>
      </div>
      <label for="pol-allow">Allowed destinations <span class="label-hint">Default-deny when set · one host per line · ".example.com" matches any subdomain</span></label>
      <textarea id="pol-allow" value={allow} onInput={(e) => setAllow((e.target as HTMLTextAreaElement).value)} placeholder="api.anthropic.com&#10;.github.com" />
      <label for="pol-deny">Always blocked <span class="label-hint">Wins over allowed · one host per line</span></label>
      <textarea id="pol-deny" value={deny} onInput={(e) => setDeny((e.target as HTMLTextAreaElement).value)} placeholder="metadata.google.internal" />
      <label for="pol-methods">Per-host HTTP methods <span class="label-hint">JSON · limits which methods each host accepts</span></label>
      <textarea id="pol-methods" value={methods} onInput={(e) => setMethods((e.target as HTMLTextAreaElement).value)} placeholder={'{"git.internal": ["GET", "POST"]}'} />

      <div class="dryrun">
        <div class="dryrun__head">
          <span class="dryrun__title">Dry-run against recent traffic</span>
          <span class="dryrun__stat">
            {impact.total} recent request{impact.total === 1 ? "" : "s"} ·{" "}
            <span class={impact.denied ? "dryrun__deny" : "dryrun__ok"}>{impact.denied} would be denied</span>
          </span>
        </div>
        {impact.total === 0
          ? <div class="dryrun__empty">No recent egress to evaluate yet.</div>
          : impact.denied === 0
            ? <div class="dryrun__pass"><Icon name="check" size={14} /> Every recent request passes these rules.</div>
            : <ul class="dryrun__list">
                {impact.rows.slice(0, 8).map((r) => (
                  <li key={r.method + r.upstream}>
                    <span class="decision d-deny">deny</span>
                    <span class="c-mono dryrun__dest">{r.method ? r.method + " " : ""}{r.upstream}</span>
                    <span class="dryrun__reason">{r.reason}</span>
                    <span class="dryrun__n">×{r.count}</span>
                  </li>
                ))}
                {impact.rows.length > 8 && <li class="dryrun__more">+{impact.rows.length - 8} more destinations</li>}
              </ul>}
        <p class="dryrun__note">Evaluates allow/deny and method rules against the recent audit trail. Secret redaction depends on request contents and is not simulated.</p>
      </div>

      {err && <div class="err">{err}</div>}
      <div class="actions">
        <button class="btn btn--primary" onClick={save}>Save</button>
        {policy.name && <button class="btn btn--danger" onClick={del}>Delete</button>}
      </div>
      <div class="hint">Reference from a pod: <code>poddle up --policy {name || "&lt;name&gt;"}</code>, or <code>policy = "{name || "&lt;name&gt;"}"</code> in a template.</div>
    </div>
  );
}

function PolicyView({ selected, events }: { selected?: string; events: Event[] }) {
  const [policies, setPolicies] = useState<Policy[]>([]);
  const [loading, setLoading] = useState(true);
  const { pods } = usePods();
  const load = () => api.policies().then((ps: Policy[]) => setPolicies(ps || [])).catch(() => setPolicies([])).finally(() => setLoading(false));
  useEffect(() => { load(); }, []);

  // Fleet governance: how many running pods each policy governs, and which run
  // with none (a real risk — an unpoliced pod's egress is unrestricted).
  const running = pods.filter((p) => p.state === "running");
  const usage = (name: string) => running.filter((p) => p.policy === name).length;
  const ungoverned = running.filter((p) => !p.policy);

  // The selected policy is URL-driven (/policies/:name; "new" is the blank draft).
  const sel: Policy | null =
    selected === "new" ? { name: "", egress: "redact" }
      : selected ? (policies.find((p) => p.name === selected) || null)
        : null;

  return (
    <div>
      {ungoverned.length > 0 && (
        <div class="insight insight--warn">
          <span class="insight__icon" aria-hidden="true"><Icon name="ban" size={16} /></span>
          <span class="insight__text">
            <strong>{ungoverned.length} running pod{ungoverned.length === 1 ? "" : "s"} with no policy.</strong>{" "}
            {ungoverned.length === 1 ? "Its" : "Their"} egress is unrestricted. Bind a policy to govern {ungoverned.length === 1 ? "it" : "them"}:{" "}
            {ungoverned.map((p, i) => (
              <span key={p.name}>{i > 0 ? ", " : ""}<a class="insight__pod" href={`/pods/${encodeURIComponent(p.name)}`} onClick={linkTo(`/pods/${encodeURIComponent(p.name)}`)}>{p.name}</a></span>
            ))}
          </span>
        </div>
      )}
      <div class="layout">
        <div class="list">
          {loading
            ? [0, 1, 2].map((i) => <span class="list__skel skel" key={i} aria-hidden="true" />)
            : policies.map((p) => {
                const n = usage(p.name);
                return (
                  <a key={p.name} href={`/policies/${encodeURIComponent(p.name)}`} onClick={linkTo(`/policies/${encodeURIComponent(p.name)}`)}
                    class={"list__row" + (selected === p.name ? " on" : "")}>
                    <span>{p.name}</span>
                    {n > 0 && <span class="list__meta" title={`${n} running pod${n === 1 ? "" : "s"} use this policy`}>{n} pod{n === 1 ? "" : "s"}</span>}
                  </a>
                );
              })}
          <a href="/policies/new" onClick={linkTo("/policies/new")} class="new">＋ New policy</a>
        </div>
        {sel
          ? <PolicyEditor policy={sel} events={events}
              onSaved={(name) => { load(); navigate(`/policies/${encodeURIComponent(name)}`); }}
              onDeleted={() => { load(); navigate("/policies"); }} />
          : <div class="editor empty">Select a policy, or create one.</div>}
      </div>
    </div>
  );
}

// The primary nav lives in the left rail; each item is a real <a> (deep-links,
// middle-click opens a tab) paired with its glyph.
const NAV = [
  { to: "/overview", key: "overview", label: "Overview", icon: "overview" },
  { to: "/pods", key: "pods", label: "Pods", icon: "pods" },
  { to: "/audit", key: "audit", label: "Audit", icon: "audit" },
  { to: "/destinations", key: "destinations", label: "Destinations", icon: "globe" },
  { to: "/policies", key: "policies", label: "Policies", icon: "policies" },
];
// Each section names itself in the top bar, in the product's own voice.
const PAGE: Record<string, { title: string; sub: string }> = {
  overview: { title: "Overview", sub: "Every agent, every request, accounted for." },
  pods: { title: "Pods", sub: "Live sandboxes and what they are using." },
  audit: { title: "Audit", sub: "The tamper-evident log of every egress decision." },
  destinations: { title: "Destinations", sub: "Where your agents are reaching, and how the broker ruled." },
  policies: { title: "Policies", sub: "The egress rules your pods run under." },
};

function Sidebar({ active, v, collapsed }: { active: string; v: Verify; collapsed: boolean }) {
  return (
    <aside class="sidebar">
      <a class="brand" href="/overview" aria-label="poddle" onClick={linkTo("/overview")}>
        <PoddleMark size={27} />
        <span class="brand__name">poddle</span>
      </a>
      <nav class="nav" aria-label="Primary">
        {NAV.map((it) => (
          <a key={it.key} href={it.to} class={"nav__i" + (active === it.key ? " on" : "")}
            title={collapsed ? it.label : undefined}
            aria-current={active === it.key ? "page" : undefined} onClick={linkTo(it.to)}>
            <Icon name={it.icon} size={17} /><span>{it.label}</span>
          </a>
        ))}
      </nav>
      <div class="sidebar__foot">
        <VerifyBadge v={v} compact={collapsed} />
        <ThemeToggle />
      </div>
    </aside>
  );
}

// goPod routes to a pod's drill-down page.
const goPod = (pod: string) => navigate("/pods/" + encodeURIComponent(pod));

function Fact({ label, children }: { label: string; children: any }) {
  return <div><dt>{label}</dt><dd>{children}</dd></div>;
}

// PodControls are the mutating actions on a live pod, both confirmed inline:
// rebind its governing policy (POST …/policy) and revoke its credentials
// (DELETE …). The pod poll (3s) reflects the new binding on its own.
type Pending = { type: "bind"; name: string } | { type: "revoke" } | null;
function PodControls({ pod, policies }: { pod: Pod; policies: Policy[] }) {
  const [pending, setPending] = useState<Pending>(null);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<{ ok: boolean; msg: string } | null>(null);

  const bind = async (name: string) => {
    const p = policies.find((x) => x.name === name);
    if (!p) return;
    setBusy(true);
    const res = await api.bindPodPolicy(pod.name, p).catch(() => null);
    setBusy(false); setPending(null);
    setStatus(res && res.ok ? { ok: true, msg: `Now governed by ${name}.` } : { ok: false, msg: `Could not bind ${name}.` });
  };
  const revoke = async () => {
    setBusy(true);
    const res = await api.revokePod(pod.name).catch(() => null);
    setBusy(false); setPending(null);
    setStatus(res && res.ok ? { ok: true, msg: "Credentials revoked." } : { ok: false, msg: "Could not revoke credentials." });
  };

  return (
    <div class="controls">
      <div class="controls__row">
        <div class="controls__label">Governed by</div>
        <div class="chips">
          {policies.length === 0
            ? <span class="faint">No policies defined yet.</span>
            : policies.map((p) => (
                <button key={p.name} type="button" disabled={busy || pod.policy === p.name}
                  class={"chip" + (pod.policy === p.name ? " chip--on" : "")}
                  onClick={() => { setStatus(null); setPending({ type: "bind", name: p.name }); }}>
                  {p.name}{pod.policy === p.name && <span class="chip__now"> · current</span>}
                </button>
              ))}
        </div>
      </div>
      <div class="controls__row">
        <div class="controls__label">Credentials</div>
        <button type="button" class="btn btn--danger btn--sm" disabled={busy}
          onClick={() => { setStatus(null); setPending({ type: "revoke" }); }}>Revoke credentials</button>
      </div>

      {pending && (
        <div class="controls__confirm">
          <span class="controls__confirmtext">
            {pending.type === "bind"
              ? <>Bind policy <strong>{pending.name}</strong> to <strong>{pod.name}</strong>? The gateway enforces it on the pod's next request.</>
              : <>Revoke every credential issued to <strong>{pod.name}</strong>? Its brokered secrets stop working immediately.</>}
          </span>
          <div class="controls__confirmbtns">
            <button type="button" disabled={busy}
              class={"btn btn--sm " + (pending.type === "revoke" ? "btn--danger" : "btn--primary")}
              onClick={() => (pending.type === "bind" ? bind(pending.name) : revoke())}>
              {busy ? "Working…" : pending.type === "bind" ? "Bind" : "Revoke"}
            </button>
            <button type="button" class="btn btn--ghost btn--sm" disabled={busy} onClick={() => setPending(null)}>Cancel</button>
          </div>
        </div>
      )}
      {status && <div class={"controls__status " + (status.ok ? "ok" : "bad")} role="status">{status.msg}</div>}
    </div>
  );
}

function PodDetailView({ name, events, loading }: { name: string; events: Event[]; loading: boolean }) {
  const { pods, hist } = usePods();
  const [policies, setPolicies] = useState<Policy[]>([]);
  useEffect(() => { api.policies().then((ps: Policy[]) => setPolicies(ps || [])).catch(() => {}); }, []);
  const pod = pods.find((p) => p.name === name);
  const h = hist[name] || { cpu: [], mem: [] };
  return (
    <div>
      <div class="detail-head">
        <a href="/pods" class="back" onClick={linkTo("/pods")}>← Pods</a>
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
              ? <a class="fact-link c-mono" href={`/policies/${encodeURIComponent(pod.policy)}`} onClick={linkTo(`/policies/${encodeURIComponent(pod.policy)}`)}>{pod.policy}</a>
              : <span class="faint">none</span>}
          </Fact>
          <Fact label="cpu"><span class="perf-inline"><Spark data={h.cpu} /><span class="c-mono">{pod.cpu || "—"}</span></span></Fact>
          <Fact label="memory"><span class="perf-inline"><Spark data={h.mem} /><span class="c-mono">{pod.mem || "—"}</span></span></Fact>
        </dl>
      )}

      {pod && pod.state === "running" && (
        <>
          <h2 class="section-title">Controls</h2>
          <PodControls pod={pod} policies={policies} />
        </>
      )}

      <h2 class="section-title">Audit trail</h2>
      <AuditView events={events} initialPod={name} loading={loading} />
    </div>
  );
}

// ThemeToggle flips light/dark and persists the choice. The initial attribute is
// set by an inline script in index.html (before paint), so there is no flash.
function ThemeToggle() {
  const [theme, setTheme] = useState(
    () => (typeof document !== "undefined" && document.documentElement.getAttribute("data-theme")) || "light",
  );
  const apply = (t: string) => {
    document.documentElement.setAttribute("data-theme", t);
    try { localStorage.setItem("poddle-theme", t); } catch {}
    setTheme(t);
  };
  const dark = theme === "dark";
  return (
    <button class="theme-toggle" type="button" aria-pressed={dark} title={dark ? "Light theme" : "Dark theme"}
      aria-label={dark ? "Switch to light theme" : "Switch to dark theme"}
      onClick={() => apply(dark ? "light" : "dark")}>
      {dark
        ? <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></svg>
        : <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" /></svg>}
    </button>
  );
}

// CommandPalette is a ⌘K/Ctrl-K launcher: fuzzy-jump to any view, pod, policy,
// or destination. Pods/policies are fetched once on open; destinations come from
// the audit stream already in memory.
type Cmd = { id: string; label: string; hint: string; icon: string; run: () => void };
function CommandPalette({ open, onClose, events }: { open: boolean; onClose: () => void; events: Event[] }) {
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(0);
  const [pods, setPods] = useState<Pod[]>([]);
  const [pols, setPols] = useState<Policy[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    setQ(""); setSel(0);
    api.pods().then((p: Pod[]) => setPods(p || [])).catch(() => {});
    api.policies().then((p: Policy[]) => setPols(p || [])).catch(() => {});
    const id = setTimeout(() => inputRef.current?.focus(), 0);
    return () => clearTimeout(id);
  }, [open]);

  const cmds: Cmd[] = useMemo(() => {
    const nav: Cmd[] = NAV.map((n) => ({ id: "nav:" + n.key, label: n.label, hint: "view", icon: n.icon, run: () => navigate(n.to) }));
    const podCmds: Cmd[] = pods.map((p) => ({ id: "pod:" + p.name, label: p.name, hint: "pod", icon: "pods", run: () => navigate("/pods/" + encodeURIComponent(p.name)) }));
    const polCmds: Cmd[] = pols.map((p) => ({ id: "pol:" + p.name, label: p.name, hint: "policy", icon: "policies", run: () => navigate("/policies/" + encodeURIComponent(p.name)) }));
    const destCmds: Cmd[] = destinations(events).slice(0, 20).map((d) => ({ id: "dest:" + d.upstream, label: d.upstream, hint: "destination", icon: "globe", run: () => navigate("/audit?q=" + encodeURIComponent(d.upstream)) }));
    const theme: Cmd = {
      id: "theme", label: "Toggle light / dark theme", hint: "action", icon: "theme",
      run: () => { const r = document.documentElement; const t = r.getAttribute("data-theme") === "dark" ? "light" : "dark"; r.setAttribute("data-theme", t); try { localStorage.setItem("poddle-theme", t); } catch {} },
    };
    return [...nav, ...podCmds, ...polCmds, ...destCmds, theme];
  }, [pods, pols, events]);

  const s = q.toLowerCase();
  const shown = q ? cmds.filter((c) => c.label.toLowerCase().includes(s) || c.hint.includes(s)) : cmds;
  const selClamped = Math.min(sel, Math.max(0, shown.length - 1));

  if (!open) return null;

  const run = (c: Cmd) => { onClose(); c.run(); };
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setSel((i) => Math.min(i + 1, shown.length - 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setSel((i) => Math.max(i - 1, 0)); }
    else if (e.key === "Enter") { e.preventDefault(); if (shown[selClamped]) run(shown[selClamped]); }
    else if (e.key === "Escape") { e.preventDefault(); onClose(); }
  };

  return (
    <div class="cmdk" role="dialog" aria-modal="true" aria-label="Command palette" onClick={onClose}>
      <div class="cmdk__panel" onClick={(e) => e.stopPropagation()}>
        <div class="cmdk__search">
          <span class="cmdk__searchic" aria-hidden="true"><Icon name="search" size={16} /></span>
          <input ref={inputRef} class="cmdk__input" placeholder="Jump to a view, pod, policy, or destination…"
            value={q} aria-label="Command palette search"
            onInput={(e) => { setQ((e.target as HTMLInputElement).value); setSel(0); }} onKeyDown={onKey} />
        </div>
        <ul class="cmdk__list">
          {shown.length === 0 && <li class="cmdk__empty">No matches.</li>}
          {shown.slice(0, 40).map((c, i) => (
            <li key={c.id}>
              <button type="button" class={"cmdk__item" + (i === selClamped ? " on" : "")}
                onMouseEnter={() => setSel(i)} onClick={() => run(c)}>
                <span class="cmdk__ic" aria-hidden="true"><Icon name={c.icon} size={15} /></span>
                <span class="cmdk__lb">{c.label}</span>
                <span class="cmdk__hint">{c.hint}</span>
              </button>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

// ToastHost surfaces live denials/blocks the moment they stream in, so the
// console tells you rather than waiting to be checked. Each links to the audit.
type Toast = { id: number; pod: string; decision: string; upstream: string };
function ToastHost({ toasts, onDismiss }: { toasts: Toast[]; onDismiss: (id: number) => void }) {
  if (toasts.length === 0) return null;
  return (
    <div class="toasts" role="region" aria-label="Live alerts">
      {toasts.map((t) => {
        const to = "/audit?q=" + encodeURIComponent(t.upstream || t.pod);
        return (
          <div key={t.id} class="toast" role="status">
            <span class="toast__ic" aria-hidden="true"><Icon name={t.decision === "block" ? "octagon" : "ban"} size={16} /></span>
            <div class="toast__body">
              <div class="toast__title"><span class="c-pod">{t.pod}</span> <span class={"decision d-" + t.decision}>{t.decision}</span></div>
              <a class="toast__link c-mono" href={to} onClick={linkTo(to)}>{t.upstream || "egress"}</a>
            </div>
            <button type="button" class="toast__close" aria-label="Dismiss alert" onClick={() => onDismiss(t.id)}>×</button>
          </div>
        );
      })}
    </div>
  );
}

function App() {
  const route = useRoute();
  const [toasts, setToasts] = useState<Toast[]>([]);
  const dismiss = (id: number) => setToasts((t) => t.filter((x) => x.id !== id));
  const pushToast = (ev: Event) => {
    if (ev.decision !== "deny" && ev.decision !== "block") return;
    // Ignore any replay of already-old events on (re)connect; only alert on fresh ones.
    if (ev.time && Date.now() - new Date(ev.time).getTime() > 60000) return;
    const id = ev.seq;
    setToasts((prev) => (prev.some((x) => x.id === id) ? prev : [...prev, { id, pod: ev.pod || "—", decision: ev.decision as string, upstream: ev.upstream || "" }].slice(-4)));
    setTimeout(() => dismiss(id), 6500);
  };
  const { events, loading: eventsLoading, status: liveStatus } = useAudit(pushToast);
  const vf = useVerify();
  const active = route.view === "pod" ? "pods" : route.view;
  const page = PAGE[active] || PAGE.overview;
  const [collapsed, setCollapsed] = useState(() => {
    try { return localStorage.getItem("poddle-sidebar") === "collapsed"; } catch { return false; }
  });
  const toggleRail = () => setCollapsed((c) => {
    const n = !c;
    try { localStorage.setItem("poddle-sidebar", n ? "collapsed" : "expanded"); } catch {}
    return n;
  });

  // Reflect the section in the tab title so history/tab-switching are legible.
  const docName = route.view === "pod" ? route.name : page.title;
  useEffect(() => { document.title = "poddle · " + docName; }, [docName]);

  // ⌘K / Ctrl-K toggles the command palette from anywhere; Escape closes it.
  const [paletteOpen, setPaletteOpen] = useState(false);
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && (e.key === "k" || e.key === "K")) { e.preventDefault(); setPaletteOpen((o) => !o); }
      else if (e.key === "Escape") setPaletteOpen(false);
    };
    addEventListener("keydown", on);
    return () => removeEventListener("keydown", on);
  }, []);

  return (
    <div class={"app" + (collapsed ? " app--collapsed" : "")}>
      <Sidebar active={active} v={vf.verify} collapsed={collapsed} />
      <div class="content">
        <header class="topbar">
          <button class="rail-toggle" type="button" aria-expanded={!collapsed}
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"} onClick={toggleRail}>
            <Icon name="panel" size={18} />
          </button>
          <div class="topbar__head">
            <h1 class="topbar__title">{page.title}</h1>
            <p class="topbar__sub">{page.sub}</p>
          </div>
          <div class="topbar__actions">
            <button class="topbar__search" type="button" aria-label="Open command palette" onClick={() => setPaletteOpen(true)}>
              <Icon name="search" size={15} /><span class="topbar__searchlabel">Search</span><kbd class="topbar__kbd">{CMD_HINT}</kbd>
            </button>
            <LiveDot status={liveStatus} />
          </div>
        </header>
        <main>
          {route.view === "overview" && <OverviewView events={events} loading={eventsLoading} onPod={goPod} />}
          {route.view === "pods" && <PodsView onPod={goPod} />}
          {route.view === "pod" && <PodDetailView name={route.name} events={events} loading={eventsLoading} />}
          {route.view === "audit" && (
            <>
              <IntegrityPanel verify={vf.verify} checkedAt={vf.checkedAt} recheck={vf.recheck} count={events.length} />
              <AuditView events={events} initialPod={route.pod} initialQ={route.q} loading={eventsLoading} />
            </>
          )}
          {route.view === "destinations" && <DestinationsView events={events} loading={eventsLoading} />}
          {route.view === "policies" && <PolicyView selected={route.name} events={events} />}
        </main>
      </div>
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} events={events} />
      <ToastHost toasts={toasts} onDismiss={dismiss} />
    </div>
  );
}

render(<App />, document.getElementById("app")!);
