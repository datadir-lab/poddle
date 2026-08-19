import { render } from "preact";
import { useEffect, useMemo, useState } from "preact/hooks";
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
};

// ---- router ----
// A tiny dependency-free history router. The Go handler serves the SPA shell for
// any non-asset path, so these URLs deep-link and survive a refresh.
type Route =
  | { view: "overview" }
  | { view: "pods" }
  | { view: "pod"; name: string }
  | { view: "audit"; pod?: string }
  | { view: "policies"; name?: string };

function parseRoute(path: string): Route {
  const [p, qs] = path.split("?");
  const seg = p.split("/").filter(Boolean);
  const query = new URLSearchParams(qs || "");
  switch (seg[0]) {
    case "pods":
      return seg[1] ? { view: "pod", name: decodeURIComponent(seg[1]) } : { view: "pods" };
    case "audit":
      return { view: "audit", pod: query.get("pod") || undefined };
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
// the Overview and Audit views so there is one subscription.
function useAudit(): Event[] {
  const [events, setEvents] = useState<Event[]>([]);
  useEffect(() => {
    api.audit().then((es: Event[]) => setEvents(es || [])).catch(() => {});
    const src = new EventSource(`${CFG.apiBase}/audit/stream`);
    src.onmessage = (e) => {
      try { const ev = JSON.parse(e.data); setEvents((prev) => [ev, ...prev].slice(0, 4000)); } catch {}
    };
    return () => src.close();
  }, []);
  return events;
}

type Verify = { ok: boolean; brokenAt: number } | null;
function useVerify(): Verify {
  const [v, setV] = useState<Verify>(null);
  useEffect(() => {
    const tick = () => api.verify().then(setV).catch(() => setV(null));
    tick();
    const id = setInterval(tick, 15000);
    return () => clearInterval(id);
  }, []);
  return v;
}

// usePods polls /v1/pods and keeps a rolling CPU/mem history per pod for the
// sparklines (the browser is the time-series store — no server needed).
type Hist = Record<string, { cpu: number[]; mem: number[] }>;
function usePods(): { pods: Pod[]; hist: Hist } {
  const [pods, setPods] = useState<Pod[]>([]);
  const [hist, setHist] = useState<Hist>({});
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
    }).catch(() => {});
    tick();
    const id = setInterval(tick, 3000);
    return () => clearInterval(id);
  }, []);
  return { pods, hist };
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
function VerifyBadge({ v }: { v: Verify }) {
  if (!v) return <span class="badge">verifying…</span>;
  return v.ok
    ? <span class="badge ok">chain intact ✓</span>
    : <span class="badge bad">chain broken @{v.brokenAt} ✗</span>;
}

function Card({ n, label, tone }: { n: number | string; label: string; tone?: string }) {
  return (
    <div class={"card" + (tone ? " card--" + tone : "")}>
      <div class="card__num">{n}</div>
      <div class="card__label">{label}</div>
    </div>
  );
}

// Segmented is an accessible single-select control (role=radiogroup) for a
// small set of mutually exclusive options that should all stay visible with
// immediate effect — the right pattern for egress mode and the audit filter,
// and it keeps the bundle dependency-free for go:embed. An option's `tone`
// colors the active segment by its meaning (e.g. block = deny-red).
type SegOption = { value: string; label: string; tone?: string };
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
          {o.label}
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

function OverviewView({ events, onPod }: { events: Event[]; onPod: (pod: string) => void }) {
  const { pods: livePods } = usePods(); // live fleet, not audit history
  const s = useMemo(() => summarise(events), [events]);
  const attention = useMemo(() => group(events, ["deny", "block"]).slice(0, 8), [events]);
  const redactions = useMemo(() => group(events, ["redact"]).slice(0, 12), [events]);

  return (
    <div>
      <div class="cards">
        <Card n={livePods.length} label="pods active" />
        <Card n={s.requests} label="requests" />
        <Card n={s.secrets} label="secrets redacted" tone={s.secrets ? "warn" : undefined} />
        <Card n={s.blocked + s.denied} label="blocked / denied" tone={s.blocked + s.denied ? "flag" : undefined} />
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
                  <tr onClick={() => onPod(c.pod)} class="clickable">
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
  const { pods, hist } = usePods();
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
              <tr key={p.name} class="clickable" onClick={() => onPod(p.name)}>
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

function AuditView({ events, initialPod }: { events: Event[]; initialPod?: string }) {
  const [q, setQ] = useState(initialPod || "");
  const [decision, setDecision] = useState("");
  useEffect(() => { if (initialPod) setQ(initialPod); }, [initialPod]);

  const shown = useMemo(() => events.filter((e) => {
    if (decision && e.decision !== decision) return false;
    if (!q) return true;
    const s = q.toLowerCase();
    return (e.pod || "").toLowerCase().includes(s) || (e.kind || "").toLowerCase().includes(s) ||
      (e.upstream || "").toLowerCase().includes(s);
  }), [events, q, decision]);

  return (
    <div>
      <div class="toolbar">
        <input class="grow" placeholder="Filter by pod, kind, or upstream…" value={q}
          onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
        <Segmented value={decision} options={DECISION_FILTER} onChange={setDecision} ariaLabel="filter by decision" />
        <span class="count">{shown.length} events</span>
      </div>
      <div class="table-wrap">
        <table class="dense">
          <thead>
            <tr><th scope="col">time</th><th scope="col">pod</th><th scope="col">kind</th><th scope="col">decision</th><th scope="col">upstream</th><th scope="col">detail</th></tr>
          </thead>
          <tbody>
            {shown.length === 0 && (
              <tr><td colSpan={6} class="empty">
                {q || decision ? "No events match your filter." : "Monitoring active — no events recorded yet."}
              </td></tr>
            )}
            {shown.slice(0, 800).map((e) => (
              <tr key={e.seq}>
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

const lines = (a?: string[]) => (a || []).join("\n");
const parseLines = (s: string) => s.split("\n").map((x) => x.trim()).filter(Boolean);

function PolicyEditor({ policy, onSaved, onDeleted }: { policy: Policy; onSaved: (name: string) => void; onDeleted: () => void }) {
  const [name, setName] = useState(policy.name);
  const [allow, setAllow] = useState(lines(policy.allow_upstreams));
  const [deny, setDeny] = useState(lines(policy.deny_upstreams));
  const [egress, setEgress] = useState(policy.egress || "redact");
  const [methods, setMethods] = useState(JSON.stringify(policy.methods || {}, null, 2));
  const [err, setErr] = useState("");

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
          <label>Name</label>
          <input value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        </div>
        <div class="narrow">
          <label>Egress mode</label>
          <Segmented value={egress} options={EGRESS_MODES} onChange={setEgress} ariaLabel="egress mode" />
        </div>
      </div>
      <label>Allowed destinations <span class="label-hint">Default-deny when set · one host per line · ".example.com" matches any subdomain</span></label>
      <textarea value={allow} onInput={(e) => setAllow((e.target as HTMLTextAreaElement).value)} placeholder="api.anthropic.com&#10;.github.com" />
      <label>Always blocked <span class="label-hint">Wins over allowed · one host per line</span></label>
      <textarea value={deny} onInput={(e) => setDeny((e.target as HTMLTextAreaElement).value)} placeholder="metadata.google.internal" />
      <label>Per-host HTTP methods <span class="label-hint">JSON · limits which methods each host accepts</span></label>
      <textarea value={methods} onInput={(e) => setMethods((e.target as HTMLTextAreaElement).value)} placeholder={'{"git.internal": ["GET", "POST"]}'} />
      {err && <div class="err">{err}</div>}
      <div class="actions">
        <button class="btn btn--primary" onClick={save}>Save</button>
        {policy.name && <button class="btn btn--danger" onClick={del}>Delete</button>}
      </div>
      <div class="hint">Reference from a pod: <code>poddle up --policy {name || "&lt;name&gt;"}</code>, or <code>policy = "{name || "&lt;name&gt;"}"</code> in a template.</div>
    </div>
  );
}

function PolicyView({ selected }: { selected?: string }) {
  const [policies, setPolicies] = useState<Policy[]>([]);
  const load = () => api.policies().then((ps: Policy[]) => setPolicies(ps || [])).catch(() => setPolicies([]));
  useEffect(() => { load(); }, []);

  // The selected policy is URL-driven (/policies/:name; "new" is the blank draft).
  const sel: Policy | null =
    selected === "new" ? { name: "", egress: "redact" }
      : selected ? (policies.find((p) => p.name === selected) || null)
        : null;

  return (
    <div class="layout">
      <div class="list">
        {policies.map((p) => (
          <a key={p.name} href={`/policies/${encodeURIComponent(p.name)}`} onClick={linkTo(`/policies/${encodeURIComponent(p.name)}`)}
            class={selected === p.name ? "on" : ""}>{p.name}</a>
        ))}
        <a href="/policies/new" onClick={linkTo("/policies/new")} class="new">＋ New policy</a>
      </div>
      {sel
        ? <PolicyEditor policy={sel}
            onSaved={(name) => { load(); navigate(`/policies/${encodeURIComponent(name)}`); }}
            onDeleted={() => { load(); navigate("/policies"); }} />
        : <div class="editor empty">Select a policy, or create one.</div>}
    </div>
  );
}

function NavLink({ to, active, children }: { to: string; active: boolean; children: any }) {
  return <a href={to} class={active ? "on" : ""} onClick={linkTo(to)}>{children}</a>;
}

// goPod routes to a pod's drill-down page.
const goPod = (pod: string) => navigate("/pods/" + encodeURIComponent(pod));

function Fact({ label, children }: { label: string; children: any }) {
  return <div><dt>{label}</dt><dd>{children}</dd></div>;
}

function PodDetailView({ name, events }: { name: string; events: Event[] }) {
  const { pods, hist } = usePods();
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

      <h2 class="section-title">Audit trail</h2>
      <AuditView events={events} initialPod={name} />
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

function App() {
  const route = useRoute();
  const events = useAudit();
  const v = useVerify();
  const active = route.view === "pod" ? "pods" : route.view;

  return (
    <div>
      <header>
        <a class="brand" href="/overview" onClick={linkTo("/overview")}>
          <span class="brand__name">poddle</span>
        </a>
        <nav>
          <NavLink to="/overview" active={active === "overview"}>Overview</NavLink>
          <NavLink to="/pods" active={active === "pods"}>Pods</NavLink>
          <NavLink to="/audit" active={active === "audit"}>Audit</NavLink>
          <NavLink to="/policies" active={active === "policies"}>Policies</NavLink>
        </nav>
        <VerifyBadge v={v} />
        <ThemeToggle />
      </header>
      <main>
        {route.view === "overview" && <OverviewView events={events} onPod={goPod} />}
        {route.view === "pods" && <PodsView onPod={goPod} />}
        {route.view === "pod" && <PodDetailView name={route.name} events={events} />}
        {route.view === "audit" && <AuditView events={events} initialPod={route.pod} />}
        {route.view === "policies" && <PolicyView selected={route.name} />}
      </main>
    </div>
  );
}

render(<App />, document.getElementById("app")!);
