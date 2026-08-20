import { render } from "preact";
import { useEffect, useMemo, useState } from "preact/hooks";
import "@fontsource-variable/inter";
import "@fontsource-variable/fraunces/full.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource/jetbrains-mono/600.css";
import "./style.css";
import {
  SegmentedControl, IntegrityBadge, AuditLogTable,
  PodFleetTable, PodDetailPanel, OverviewCards, AttentionPanel, RedactionsTable, PolicyList,
  summarise, group,
  type SegOption,
} from "@poddle/ui/views";

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

// ---- views ----
const EGRESS_MODES: SegOption[] = [
  { value: "redact", label: "Redact", tone: "redact" },
  { value: "block", label: "Block", tone: "deny" },
  { value: "off", label: "Off", tone: "faint" },
];

function OverviewView({ events, onPod }: { events: Event[]; onPod: (pod: string) => void }) {
  const { pods: livePods } = usePods(); // live fleet, not audit history
  const s = useMemo(() => summarise(events), [events]);
  const stats = { ...s, pods: livePods.length }; // "pods active" is the LIVE fleet, not audit history
  const attention = useMemo(() => group(events, ["deny", "block"]).slice(0, 8), [events]);
  const redactions = useMemo(() => group(events, ["redact"]).slice(0, 12), [events]);

  return (
    <div>
      <OverviewCards stats={stats} />
      <AttentionPanel attention={attention} onPod={onPod} />
      <RedactionsTable redactions={redactions} onPod={onPod} />
    </div>
  );
}

function PodsView({ onPod }: { onPod: (pod: string) => void }) {
  const { pods, hist } = usePods();
  return (
    <PodFleetTable pods={pods} hist={hist} onPod={onPod}
      emptyState={<>No pods running yet — start one with <code>poddle up</code>.</>} />
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
          <label for="pol-name">Name</label>
          <input id="pol-name" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        </div>
        <div class="narrow">
          <label>Egress mode</label>
          <SegmentedControl value={egress} options={EGRESS_MODES} onChange={setEgress} ariaLabel="egress mode" />
        </div>
      </div>
      <label for="pol-allow">Allowed destinations <span class="label-hint">Default-deny when set · one host per line · ".example.com" matches any subdomain</span></label>
      <textarea id="pol-allow" value={allow} onInput={(e) => setAllow((e.target as HTMLTextAreaElement).value)} placeholder="api.anthropic.com&#10;.github.com" />
      <label for="pol-deny">Always blocked <span class="label-hint">Wins over allowed · one host per line</span></label>
      <textarea id="pol-deny" value={deny} onInput={(e) => setDeny((e.target as HTMLTextAreaElement).value)} placeholder="metadata.google.internal" />
      <label for="pol-methods">Per-host HTTP methods <span class="label-hint">JSON · limits which methods each host accepts</span></label>
      <textarea id="pol-methods" value={methods} onInput={(e) => setMethods((e.target as HTMLTextAreaElement).value)} placeholder={'{"git.internal": ["GET", "POST"]}'} />
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
      <PolicyList policies={policies} selectedName={selected}
        onSelect={(n) => navigate("/policies/" + encodeURIComponent(n))}
        onNew={() => navigate("/policies/new")} />
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

function PodDetailView({ name, events }: { name: string; events: Event[] }) {
  const { pods, hist } = usePods();
  const pod = pods.find((p) => p.name === name);
  const h = hist[name] || { cpu: [], mem: [] };
  return (
    <PodDetailPanel name={name} pod={pod} hist={h} events={events}
      onBack={linkTo("/pods")}
      onPolicyClick={pod?.policy ? linkTo(`/policies/${encodeURIComponent(pod.policy)}`) : undefined} />
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
        <IntegrityBadge v={v} />
        <ThemeToggle />
      </header>
      <main>
        {route.view === "overview" && <OverviewView events={events} onPod={goPod} />}
        {route.view === "pods" && <PodsView onPod={goPod} />}
        {route.view === "pod" && <PodDetailView name={route.name} events={events} />}
        {route.view === "audit" && <AuditLogTable events={events} initialPod={route.pod} />}
        {route.view === "policies" && <PolicyView selected={route.name} />}
      </main>
    </div>
  );
}

render(<App />, document.getElementById("app")!);
