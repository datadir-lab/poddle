import { render } from "preact";
import { useEffect, useMemo, useState } from "preact/hooks";
import "@fontsource-variable/inter";
import "@fontsource/instrument-serif";
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

const api = {
  audit: (limit = 1000) => fetch(`${CFG.apiBase}/audit?limit=${limit}`, { headers: H }).then((r) => r.json()),
  verify: () => fetch(`${CFG.apiBase}/audit/verify`, { headers: H }).then((r) => r.json()),
  policies: () => fetch(`${CFG.apiBase}/policies`, { headers: H }).then((r) => r.json()),
  putPolicy: (p: Policy) =>
    fetch(`${CFG.apiBase}/policies/${encodeURIComponent(p.name)}`, {
      method: "PUT", headers: { ...H, "Content-Type": "application/json" }, body: JSON.stringify(p),
    }),
  delPolicy: (name: string) =>
    fetch(`${CFG.apiBase}/policies/${encodeURIComponent(name)}`, { method: "DELETE", headers: H }),
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

// ---- views ----
function VerifyBadge({ v }: { v: Verify }) {
  if (!v) return <span class="badge">chain ?</span>;
  return v.ok
    ? <span class="badge ok">chain intact ✓</span>
    : <span class="badge bad">chain BROKEN @{v.brokenAt} ✗</span>;
}

function Card({ n, label, tone }: { n: number | string; label: string; tone?: string }) {
  return (
    <div class={"card" + (tone ? " card--" + tone : "")}>
      <div class="card__num">{n}</div>
      <div class="card__label">{label}</div>
    </div>
  );
}

function OverviewView({ events, v, onPod }: { events: Event[]; v: Verify; onPod: (pod: string) => void }) {
  const s = useMemo(() => summarise(events), [events]);
  const attention = useMemo(() => group(events, ["deny", "block"]).slice(0, 8), [events]);
  const caught = useMemo(() => group(events, ["redact", "block"]).slice(0, 12), [events]);

  return (
    <div>
      <div class="cards">
        <Card n={s.pods} label="pods active" />
        <Card n={s.requests} label="requests" />
        <Card n={s.secrets} label="secrets redacted" tone={s.secrets ? "warn" : undefined} />
        <Card n={s.blocked + s.denied} label="blocked / denied" tone={s.blocked + s.denied ? "flag" : undefined} />
        <Card n={v ? (v.ok ? "✓" : "✗") : "?"} label="audit chain" tone={v && v.ok ? "ok" : v ? "flag" : undefined} />
      </div>

      <h2 class="section-title">Attention</h2>
      {attention.length === 0
        ? <div class="panel empty">no policy denials or blocks — agents are inside their guardrails</div>
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

      <h2 class="section-title">Egress &amp; secrets</h2>
      {caught.length === 0
        ? <div class="panel empty">nothing caught yet — no secrets redacted, no egress blocked</div>
        : <div class="table-wrap">
            <table>
              <thead><tr><th>pod</th><th>action</th><th>destination</th><th>secrets</th><th>count</th></tr></thead>
              <tbody>
                {caught.map((c) => (
                  <tr onClick={() => onPod(c.pod)} class="clickable">
                    <td class="c-pod">{c.pod}</td>
                    <td><span class={"decision d-" + c.decision}>{c.decision}</span></td>
                    <td class="c-mono">{c.upstream}</td>
                    <td class="c-mono">{c.decision === "redact" ? c.secrets : <span class="faint">—</span>}</td>
                    <td class="c-mono">×{c.count}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>}
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
        <input class="grow" placeholder="filter pod / kind / upstream…" value={q}
          onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
        <select value={decision} onChange={(e) => setDecision((e.target as HTMLSelectElement).value)}>
          <option value="">all decisions</option>
          <option value="allow">allow</option>
          <option value="redact">redact</option>
          <option value="block">block</option>
          <option value="deny">deny</option>
        </select>
        <span class="count">{shown.length} events</span>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr><th scope="col">time</th><th scope="col">pod</th><th scope="col">kind</th><th scope="col">decision</th><th scope="col">upstream</th><th scope="col">detail</th></tr>
          </thead>
          <tbody>
            {shown.length === 0 && <tr><td colSpan={6} class="empty">no events</td></tr>}
            {shown.slice(0, 800).map((e) => (
              <tr key={e.seq}>
                <td class="c-time">{new Date(e.time).toLocaleTimeString()}</td>
                <td class="c-pod">{e.pod || <span class="faint">—</span>}</td>
                <td class="c-mono">{e.kind}</td>
                <td><span class={"decision d-" + (e.decision || "")}>{e.decision || <span class="faint">—</span>}</span></td>
                <td class="c-mono">{e.upstream || <span class="faint">—</span>}</td>
                <td class="c-detail">{e.detail}</td>
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

function PolicyEditor({ policy, onSaved, onDeleted }: { policy: Policy; onSaved: () => void; onDeleted: () => void }) {
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
    onSaved();
  };
  const del = async () => { await api.delPolicy(policy.name); onDeleted(); };

  return (
    <div class="editor">
      <div class="row">
        <div>
          <label>name</label>
          <input value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        </div>
        <div class="narrow">
          <label>egress</label>
          <select value={egress} onChange={(e) => setEgress((e.target as HTMLSelectElement).value)}>
            <option value="redact">redact</option>
            <option value="block">block</option>
            <option value="off">off</option>
          </select>
        </div>
      </div>
      <label>allow_upstreams — default-deny when set; one host per line (".x" = any subdomain)</label>
      <textarea value={allow} onInput={(e) => setAllow((e.target as HTMLTextAreaElement).value)} />
      <label>deny_upstreams — always denied (wins)</label>
      <textarea value={deny} onInput={(e) => setDeny((e.target as HTMLTextAreaElement).value)} />
      <label>methods — per-host allowed HTTP methods (JSON)</label>
      <textarea value={methods} onInput={(e) => setMethods((e.target as HTMLTextAreaElement).value)} />
      {err && <div class="err">{err}</div>}
      <div class="actions">
        <button class="btn btn--primary" onClick={save}>Save</button>
        {policy.name && <button class="btn btn--danger" onClick={del}>Delete</button>}
      </div>
      <div class="hint">Reference from a pod: <code>poddle up --policy {name || "&lt;name&gt;"}</code>, or <code>policy = "{name || "&lt;name&gt;"}"</code> in a template.</div>
    </div>
  );
}

function PolicyView() {
  const [policies, setPolicies] = useState<Policy[]>([]);
  const [sel, setSel] = useState<Policy | null>(null);

  const load = () => api.policies().then((ps: Policy[]) => setPolicies(ps || [])).catch(() => setPolicies([]));
  useEffect(() => { load(); }, []);

  return (
    <div class="layout">
      <div class="list">
        {policies.map((p) => (
          <button key={p.name} class={sel && sel.name === p.name ? "on" : ""} onClick={() => setSel(p)}>{p.name}</button>
        ))}
        <button class="new" onClick={() => setSel({ name: "", egress: "redact" })}>＋ new policy</button>
      </div>
      {sel
        ? <PolicyEditor policy={sel} onSaved={() => { load(); }} onDeleted={() => { setSel(null); load(); }} />
        : <div class="editor empty">select a policy, or create one</div>}
    </div>
  );
}

type Tab = "overview" | "audit" | "policies";
function App() {
  const [tab, setTab] = useState<Tab>("overview");
  const [podFilter, setPodFilter] = useState<string | undefined>();
  const events = useAudit();
  const v = useVerify();

  const goPod = (pod: string) => { setPodFilter(pod); setTab("audit"); };
  const nav = (t: Tab) => { setTab(t); if (t !== "audit") setPodFilter(undefined); };

  return (
    <div>
      <header>
        <span class="brand">
          <span class="brand__name">poddle</span>
          <span class="brand__sub">governance</span>
        </span>
        <nav>
          <button class={tab === "overview" ? "on" : ""} onClick={() => nav("overview")}>Overview</button>
          <button class={tab === "audit" ? "on" : ""} onClick={() => nav("audit")}>Audit</button>
          <button class={tab === "policies" ? "on" : ""} onClick={() => nav("policies")}>Policies</button>
        </nav>
        <VerifyBadge v={v} />
      </header>
      <main>
        {tab === "overview" && <OverviewView events={events} v={v} onPod={goPod} />}
        {tab === "audit" && <AuditView events={events} initialPod={podFilter} />}
        {tab === "policies" && <PolicyView />}
      </main>
    </div>
  );
}

render(<App />, document.getElementById("app")!);
