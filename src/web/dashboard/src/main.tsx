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
  audit: (limit = 500) => fetch(`${CFG.apiBase}/audit?limit=${limit}`, { headers: H }).then((r) => r.json()),
  verify: () => fetch(`${CFG.apiBase}/audit/verify`, { headers: H }).then((r) => r.json()),
  policies: () => fetch(`${CFG.apiBase}/policies`, { headers: H }).then((r) => r.json()),
  putPolicy: (p: Policy) =>
    fetch(`${CFG.apiBase}/policies/${encodeURIComponent(p.name)}`, {
      method: "PUT", headers: { ...H, "Content-Type": "application/json" }, body: JSON.stringify(p),
    }),
  delPolicy: (name: string) =>
    fetch(`${CFG.apiBase}/policies/${encodeURIComponent(name)}`, { method: "DELETE", headers: H }),
};

function VerifyBadge() {
  const [state, setState] = useState<{ ok: boolean; brokenAt: number } | null>(null);
  useEffect(() => {
    const tick = () => api.verify().then(setState).catch(() => setState(null));
    tick();
    const id = setInterval(tick, 15000);
    return () => clearInterval(id);
  }, []);
  if (!state) return <span class="badge">chain ?</span>;
  return state.ok
    ? <span class="badge ok">chain intact ✓</span>
    : <span class="badge bad">chain BROKEN @{state.brokenAt} ✗</span>;
}

function AuditView() {
  const [events, setEvents] = useState<Event[]>([]);
  const [q, setQ] = useState("");
  const [decision, setDecision] = useState("");

  useEffect(() => {
    api.audit().then((es: Event[]) => setEvents(es || [])).catch(() => {});
    const src = new EventSource(`${CFG.apiBase}/audit/stream`);
    src.onmessage = (e) => {
      try { const ev = JSON.parse(e.data); setEvents((prev) => [ev, ...prev].slice(0, 2000)); } catch {}
    };
    return () => src.close();
  }, []);

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
            <tr><th>time</th><th>pod</th><th>kind</th><th>decision</th><th>upstream</th><th>detail</th></tr>
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

function App() {
  const [tab, setTab] = useState<"audit" | "policies">("audit");
  return (
    <div>
      <header>
        <span class="brand">
          <span class="brand__name">poddle</span>
          <span class="brand__sub">governance</span>
        </span>
        <nav>
          <button class={tab === "audit" ? "on" : ""} onClick={() => setTab("audit")}>Audit</button>
          <button class={tab === "policies" ? "on" : ""} onClick={() => setTab("policies")}>Policies</button>
        </nav>
        <VerifyBadge />
      </header>
      <main>{tab === "audit" ? <AuditView /> : <PolicyView />}</main>
    </div>
  );
}

render(<App />, document.getElementById("app")!);
