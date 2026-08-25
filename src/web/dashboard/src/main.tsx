import { render } from "preact";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import "@fontsource-variable/inter";
import "@fontsource-variable/fraunces/full.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource/jetbrains-mono/600.css";
import "./style.css";
import "@poddle/ui/views.css";
import type { Stats, Cmd, Toast, PolicyTemplate, Event, Policy, Pod } from "@poddle/ui/views";
import {
  SegmentedControl, IntegrityBadge, IntegrityPanel,
  Icon, PoddleMark, ThemeToggle, EgressChart, PostureBar, FleetLoad,
  SkelCards, SkelTable, LiveDot,
  OverviewCards, AttentionPanel, RedactionsTable, PodFleetTable, PodDetailPanel,
  AuditLogTable, PolicyList, DestinationsTable, PolicyEditor, PodControls,
  CommandPalette, ToastHost,
  summarise, group, decisionCounts, destinations,
  TIME_RANGES, RANGE_MS,
} from "@poddle/ui/views";

// Runtime data-source config: the SAME bundle serves local (defaults) and the
// enterprise cloud collector, which injects { apiBase, auth, multiHost }.
type Cfg = { apiBase: string; auth: string | null; multiHost?: boolean };
const CFG: Cfg = { apiBase: "/v1", auth: null, ...(window as any).__PODDLE__ };
const H: Record<string, string> = CFG.auth ? { Authorization: "Bearer " + CFG.auth } : {};

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
  // The default policy: applied to a pod started with no --policy.
  defaultPolicy: () => fetch(`${CFG.apiBase}/default-policy`, { headers: H }).then((r) => r.json()),
  setDefaultPolicy: (name: string) =>
    fetch(`${CFG.apiBase}/default-policy`, {
      method: "PUT", headers: { ...H, "Content-Type": "application/json" }, body: JSON.stringify({ name }),
    }),
  // Bind a policy to a live pod (the gateway enforces it on the next request).
  bindPodPolicy: (pod: string, p: Policy) =>
    fetch(`${CFG.apiBase}/pods/${encodeURIComponent(pod)}/policy`, {
      method: "POST", headers: { ...H, "Content-Type": "application/json" }, body: JSON.stringify(p),
    }),
  // Revoke every credential handle the daemon issued to a pod (a kill-switch).
  revokePod: (pod: string) =>
    fetch(`${CFG.apiBase}/pods/${encodeURIComponent(pod)}`, { method: "DELETE", headers: H }),
};

// asArray coerces a JSON response to a list, so a non-array body (a daemon error
// object, an unexpected shape) can never crash a downstream .filter/for-of.
const asArray = <T,>(x: unknown): T[] => (Array.isArray(x) ? (x as T[]) : []);

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

// A pod's "create policy from this pod" hands a suggested policy across the
// navigation to the (freshly mounted) policy view via this one-shot stash.
let pendingSeed: Policy | null = null;
const takeSeed = (): Policy | null => { const s = pendingSeed; pendingSeed = null; return s; };

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
    api.audit().then((es) => setEvents(asArray<Event>(es))).catch(() => {}).finally(() => setLoading(false));
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
    const tick = () => api.pods().then((raw) => {
      const ps = asArray<Pod>(raw);
      setPods(ps);
      setHist((h) => {
        const nh: Hist = { ...h };
        for (const p of ps) {
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

// ---- views ----

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
  // The "pods active" card reflects the LIVE fleet, not the audit-history pod
  // count (a torn-down pod can still appear in recent events).
  const stats: Stats = useMemo(() => ({ ...s, pods: livePods.length }), [s, livePods]);
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
        <SegmentedControl value={range} options={TIME_RANGES} onChange={setRange} ariaLabel="overview time range" />
      </div>
      <OverviewCards stats={stats} />

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

      <AttentionPanel attention={attention} onPod={onPod} />
      <RedactionsTable redactions={redactions} onPod={onPod} />
    </div>
  );
}

function PodsView({ onPod }: { onPod: (pod: string) => void }) {
  const { pods, hist, loading } = usePods();
  if (loading) return <SkelTable rows={5} />;
  return (
    <PodFleetTable pods={pods} hist={hist} onPod={onPod}
      emptyState={<>No pods running yet — start one with <code>poddle up</code>.</>} />
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

  return (
    <div>
      {toolbar}
      {!loading && shown.length === 0
        ? <div class="panel empty">{q ? "No destinations match your filter." : "No egress recorded yet — destinations appear as your agents make requests."}</div>
        : <DestinationsTable dests={shown} loading={loading} onSelect={goAuditFor} />}
    </div>
  );
}

// Starter templates for the most common agent-sandbox postures, ordered loosest
// to strictest. Every one deny-lists the cloud metadata endpoints (a top
// credential-theft target); most default to redact (strip secrets from the wire),
// while the fail-closed pair hard-block a request that trips a secret instead. The
// operator picks one, then tweaks and saves. Passed into the presentational
// PolicyEditor as a prop (the template set is dashboard glue, not view code).
const META_DENY = ["169.254.169.254", "metadata.google.internal"];
// GitHub serves the API + git over `.github.com`, but LFS objects, release assets,
// and raw files come from `.githubusercontent.com` — allow both or clones with LFS
// silently break.
const GITHUB = [".github.com", ".githubusercontent.com"];
const CODING_ALLOW = ["api.anthropic.com", ...GITHUB, "registry.npmjs.org", "pypi.org", "files.pythonhosted.org", "proxy.golang.org"];
const POLICY_TEMPLATES: PolicyTemplate[] = [
  {
    id: "observe", label: "Observe (log & redact)",
    hint: "Allow everything but strip secrets and block metadata. Watch Destinations, then tighten into an allow-list.",
    policy: { allow_upstreams: [], deny_upstreams: META_DENY, methods: {}, egress: "redact" },
  },
  {
    id: "container-ci", label: "Container / CI builds",
    hint: "Pull images and OS packages to build and run Dockerfiles, plus the model.",
    policy: { allow_upstreams: ["api.anthropic.com", ".docker.io", ".docker.com", "ghcr.io", ".githubusercontent.com", ".quay.io", ".pkg.dev", "deb.debian.org", ".ubuntu.com", ".alpinelinux.org"], deny_upstreams: META_DENY, methods: {}, egress: "redact" },
  },
  {
    id: "coding-agent", label: "Coding agent",
    hint: "Model + GitHub + npm/PyPI/Go. The common CI or task sandbox.",
    policy: { allow_upstreams: CODING_ALLOW, deny_upstreams: META_DENY, methods: {}, egress: "redact" },
  },
  {
    id: "github-rw", label: "GitHub read-write",
    hint: "Clone, pull, and push to GitHub (incl. LFS) plus the model.",
    policy: { allow_upstreams: ["api.anthropic.com", ...GITHUB], deny_upstreams: META_DENY, methods: {}, egress: "redact" },
  },
  {
    id: "read-only", label: "Read-only GitHub",
    hint: "Clone and read from GitHub (GET only) plus the model. No pushes.",
    policy: { allow_upstreams: ["api.anthropic.com", ...GITHUB], deny_upstreams: META_DENY, methods: { ".github.com": ["GET"], ".githubusercontent.com": ["GET"] }, egress: "redact" },
  },
  {
    id: "read-only-web", label: "Read-only web",
    hint: "Browse the whole web GET-only. Terminates TLS to enforce it on HTTPS — breaks cert-pinned apps.",
    policy: { allow_upstreams: [], deny_upstreams: META_DENY, methods: { "*": ["GET", "HEAD"] }, egress: "redact", intercept: true },
  },
  {
    id: "provider-only", label: "AI provider only",
    hint: "Reach the model and nothing else. The tightest useful posture.",
    policy: { allow_upstreams: ["api.anthropic.com", "api.openai.com", "generativelanguage.googleapis.com"], deny_upstreams: META_DENY, methods: {}, egress: "redact" },
  },
  {
    id: "high-assurance", label: "High-assurance (fail-closed)",
    hint: "Full coding reach, but any request carrying a secret is refused — fail-closed, not redacted.",
    policy: { allow_upstreams: CODING_ALLOW, deny_upstreams: META_DENY, methods: {}, egress: "block" },
  },
  {
    id: "locked-down", label: "Locked down",
    hint: "Model only, and hard-fail any request that trips a secret — not just redact.",
    policy: { allow_upstreams: ["api.anthropic.com"], deny_upstreams: META_DENY, methods: {}, egress: "block" },
  },
];

function PolicyView({ selected, events }: { selected?: string; events: Event[] }) {
  const [policies, setPolicies] = useState<Policy[]>([]);
  const [loading, setLoading] = useState(true);
  const [def, setDef] = useState<string>(""); // the default policy name ("" = none)
  const [seed, setSeed] = useState<Policy | null>(takeSeed); // Duplicate or a pod suggestion pre-fills the blank editor
  const { pods } = usePods();
  const load = () => api.policies().then((ps) => setPolicies(asArray<Policy>(ps))).catch(() => setPolicies([])).finally(() => setLoading(false));
  const loadDefault = () => api.defaultPolicy().then((d) => setDef((d && d.name) || "")).catch(() => {});
  const setDefault = async (name: string) => { setDef(name); await api.setDefaultPolicy(name).catch(() => {}); loadDefault(); };
  const [governing, setGoverning] = useState(false);
  const [governMsg, setGovernMsg] = useState("");
  useEffect(() => { load(); loadDefault(); }, []);
  useEffect(() => { if (selected !== "new") setSeed(null); }, [selected]); // a duplicate seed only lives on the new-policy view
  const duplicate = (p: Policy) => { setSeed({ ...p, name: p.name + "-copy" }); navigate("/policies/new"); };
  // Save, with rename semantics: editing a saved policy's name renames it in place
  // (write the new name, delete the old, repoint the default) rather than orphaning
  // the original under the old name.
  const savePolicy = async (p: Policy): Promise<{ ok: boolean; error?: string }> => {
    const renaming = !!selected && selected !== "new" && p.name !== selected;
    if (renaming && policies.some((x) => x.name === p.name)) {
      return { ok: false, error: `A policy named "${p.name}" already exists.` };
    }
    const r = await api.putPolicy(p);
    if (!r.ok) return { ok: false, error: "Save failed: " + r.status };
    if (renaming) {
      await api.delPolicy(selected).catch(() => {});
      if (def === selected) await api.setDefaultPolicy(p.name).catch(() => {});
    }
    load(); loadDefault(); navigate(`/policies/${encodeURIComponent(p.name)}`);
    return { ok: true };
  };

  // Fleet governance: how many running pods each policy governs, and which run
  // with none (a real risk — an unpoliced pod's egress is unrestricted).
  const running = pods.filter((p) => p.state === "running");
  const usage = (name: string) => running.filter((p) => p.policy === name).length;
  const ungoverned = running.filter((p) => !p.policy);
  // Bulk-govern: bind the default policy to every unpoliced running pod in one
  // click (the poll then reflects the new binding via the daemon overlay). Only
  // offered when a default exists and we hold its rules to POST.
  const defaultPolicy = policies.find((p) => p.name === def);
  const governAll = async () => {
    if (!defaultPolicy || ungoverned.length === 0) return;
    setGoverning(true); setGovernMsg("");
    const targets = ungoverned;
    const results = await Promise.all(targets.map((p) => api.bindPodPolicy(p.name, defaultPolicy).then((r) => r.ok).catch(() => false)));
    const ok = results.filter(Boolean).length;
    setGoverning(false);
    setGovernMsg(ok === targets.length
      ? `Bound ${def} to ${ok} pod${ok === 1 ? "" : "s"} — the fleet updates shortly.`
      : `Bound ${def} to ${ok} of ${targets.length} pods; retry the rest.`);
  };
  // Which pods run the selected policy — the dry-run scopes to their traffic so
  // it answers "what would this do to the pods it governs", not the whole fleet.
  const usingPods = useMemo(
    () => (selected && selected !== "new" ? pods.filter((p) => p.policy === selected).map((p) => p.name) : []),
    [pods, selected],
  );

  // The selected policy is URL-driven (/policies/:name; "new" is the blank draft).
  // Memoize the selected policy so its object identity is stable across the pod
  // poll and audit-stream re-renders — otherwise a fresh "new" draft (or a new
  // find result) would remount the editor and wipe what the user is typing.
  const sel: Policy | null = useMemo(
    () => selected === "new" ? (seed || { name: "", egress: "redact" })
      : selected ? (policies.find((p) => p.name === selected) || null)
        : null,
    [selected, policies, seed],
  );
  const hrefFor = (name: string) => `/policies/${encodeURIComponent(name)}`;

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
            {defaultPolicy && (
              <> · <button type="button" class="insight__action" disabled={governing} onClick={governAll}>
                {governing ? "Binding…" : `Govern ${ungoverned.length === 1 ? "it" : "all"} with ${def}`}
              </button></>
            )}
            {governMsg && <span class="insight__done" role="status">{governMsg}</span>}
          </span>
        </div>
      )}
      <div class="layout">
        <div class="list-col">
          <PolicyList policies={policies} selectedName={selected} loading={loading} usage={usage}
            hrefFor={hrefFor} newHref="/policies/new" linkTo={linkTo} defaultName={def} />
          <p class="list-note">
            {def
              ? <>New pods started without a policy use <strong>{def}</strong>.</>
              : <>New pods started without a policy run ungoverned.</>}
          </p>
        </div>
        {sel
          ? <PolicyEditor policy={sel} events={events} scopePods={usingPods}
              templates={POLICY_TEMPLATES} isSaved={selected !== "new"} isDefault={selected !== "new" && def === sel.name} onSetDefault={setDefault} onDuplicate={duplicate}
              onSave={savePolicy}
              onDelete={() => api.delPolicy(sel.name).then(() => { load(); loadDefault(); navigate("/policies"); })} />
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

function Sidebar({ active, v, collapsed, onNav }: { active: string; v: Verify; collapsed: boolean; onNav?: () => void }) {
  return (
    <aside class="sidebar">
      <a class="brand" href="/overview" aria-label="poddle" onClick={(e) => { linkTo("/overview")(e); onNav?.(); }}>
        <PoddleMark size={27} />
        <span class="brand__name">poddle</span>
      </a>
      <nav class="nav" aria-label="Primary">
        {NAV.map((it) => (
          <a key={it.key} href={it.to} class={"nav__i" + (active === it.key ? " on" : "")}
            title={collapsed ? it.label : undefined}
            aria-current={active === it.key ? "page" : undefined} onClick={(e) => { linkTo(it.to)(e); onNav?.(); }}>
            <Icon name={it.icon} size={17} /><span>{it.label}</span>
          </a>
        ))}
      </nav>
      <div class="sidebar__foot">
        <IntegrityBadge v={v} compact={collapsed} href="/audit" onClick={linkTo("/audit")} />
        <ThemeToggle />
      </div>
    </aside>
  );
}

// goPod routes to a pod's drill-down page.
const goPod = (pod: string) => navigate("/pods/" + encodeURIComponent(pod));

function PodDetailView({ name, events, loading }: { name: string; events: Event[]; loading: boolean }) {
  const { pods, hist } = usePods();
  const [policies, setPolicies] = useState<Policy[]>([]);
  // Optimistic binding: a rebind takes effect at the daemon immediately, but the
  // pods poll is up to 3s behind. Reflect the new policy at once so the "current"
  // marker moves the moment the bind succeeds; the poll then confirms it.
  const [override, setOverride] = useState<string | null>(null);
  useEffect(() => { api.policies().then((ps) => setPolicies(asArray<Policy>(ps))).catch(() => {}); }, []);
  const rawPod = pods.find((p) => p.name === name);
  const pod = rawPod && override != null ? { ...rawPod, policy: override } : rawPod;
  const h = hist[name] || { cpu: [], mem: [] };
  const controls = pod && pod.state === "running"
    ? <PodControls pod={pod} policies={policies} onRebound={setOverride}
        onBind={(name) => {
          const p = policies.find((x) => x.name === name);
          if (!p) return Promise.resolve({ ok: false, msg: "Unknown policy." });
          return api.bindPodPolicy(pod.name, p)
            .then((r) => ({ ok: !!r && r.ok, msg: r && r.ok ? `Now governed by ${name}.` : `Could not bind ${name}.` }))
            .catch(() => ({ ok: false, msg: `Could not bind ${name}.` }));
        }}
        onRevoke={() => api.revokePod(pod.name)
          .then((r) => ({ ok: !!r && r.ok, msg: r && r.ok ? "Credentials revoked." : "Could not revoke credentials." }))
          .catch(() => ({ ok: false, msg: "Could not revoke credentials." }))} />
    : undefined;
  const policyHref = pod?.policy ? `/policies/${encodeURIComponent(pod.policy)}` : undefined;
  return (
    <PodDetailPanel name={name} pod={pod} hist={h} events={events} loading={loading}
      backHref="/pods" onBack={linkTo("/pods")}
      policyHref={policyHref} onPolicyClick={policyHref ? linkTo(policyHref) : undefined}
      controls={controls}
      onSuggestPolicy={(p) => { pendingSeed = p; navigate("/policies/new"); }} />
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
  // Below 880px the rail is an off-canvas drawer this button opens/closes; at
  // desktop widths the same button collapses the rail to an icon strip instead.
  const [navOpen, setNavOpen] = useState(false);
  const onMobile = () => typeof matchMedia !== "undefined" && matchMedia("(max-width: 880px)").matches;
  const toggleRail = () => {
    if (onMobile()) { setNavOpen((o) => !o); return; }
    setCollapsed((c) => {
      const n = !c;
      try { localStorage.setItem("poddle-sidebar", n ? "collapsed" : "expanded"); } catch {}
      return n;
    });
  };

  // Reflect the section in the tab title so history/tab-switching are legible.
  const docName = route.view === "pod" ? route.name : page.title;
  useEffect(() => { document.title = "poddle · " + docName; }, [docName]);
  // Any navigation (link, palette, back button) closes the mobile drawer.
  useEffect(() => { setNavOpen(false); }, [docName]);

  // ⌘K / Ctrl-K toggles the command palette from anywhere; Escape closes it.
  const [paletteOpen, setPaletteOpen] = useState(false);
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && (e.key === "k" || e.key === "K")) { e.preventDefault(); setPaletteOpen((o) => !o); }
      else if (e.key === "Escape") { setPaletteOpen(false); setNavOpen(false); }
    };
    addEventListener("keydown", on);
    return () => removeEventListener("keydown", on);
  }, []);

  // The command palette's pod/policy commands are fetched once per open (not
  // polled) — destinations come from the audit stream already in memory.
  const [palettePods, setPalettePods] = useState<Pod[]>([]);
  const [palettePols, setPalettePols] = useState<Policy[]>([]);
  useEffect(() => {
    if (!paletteOpen) return;
    api.pods().then((p) => setPalettePods(asArray<Pod>(p))).catch(() => {});
    api.policies().then((p) => setPalettePols(asArray<Policy>(p))).catch(() => {});
  }, [paletteOpen]);
  const commands: Cmd[] = useMemo(() => {
    const nav = NAV.map((n) => ({ id: "nav:" + n.key, label: n.label, hint: "view", icon: n.icon, run: () => navigate(n.to) }));
    const podCmds = palettePods.map((p) => ({ id: "pod:" + p.name, label: p.name, hint: "pod", icon: "pods", run: () => navigate("/pods/" + encodeURIComponent(p.name)) }));
    const polCmds = palettePols.map((p) => ({ id: "pol:" + p.name, label: p.name, hint: "policy", icon: "policies", run: () => navigate("/policies/" + encodeURIComponent(p.name)) }));
    const destCmds = destinations(events).slice(0, 20).map((d) => ({ id: "dest:" + d.upstream, label: d.upstream, hint: "destination", icon: "globe", run: () => navigate("/audit?q=" + encodeURIComponent(d.upstream)) }));
    const theme: Cmd = {
      id: "theme", label: "Toggle light / dark theme", hint: "action", icon: "theme",
      run: () => { const r = document.documentElement; const t = r.getAttribute("data-theme") === "dark" ? "light" : "dark"; r.setAttribute("data-theme", t); try { localStorage.setItem("poddle-theme", t); } catch {} },
    };
    return [...nav, ...podCmds, ...polCmds, ...destCmds, theme];
  }, [palettePods, palettePols, events]);

  return (
    <div class={"app" + (collapsed ? " app--collapsed" : "") + (navOpen ? " app--nav-open" : "")}>
      <Sidebar active={active} v={vf.verify} collapsed={collapsed} onNav={() => setNavOpen(false)} />
      <button class="scrim" type="button" aria-label="Close navigation" tabIndex={navOpen ? 0 : -1} onClick={() => setNavOpen(false)} />
      <div class="content">
        <header class="topbar">
          <button class="rail-toggle" type="button" aria-label="Toggle sidebar" aria-expanded={navOpen}
            onClick={toggleRail}>
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
              <AuditLogTable events={events} initialPod={route.pod} initialQ={route.q} loading={eventsLoading} />
            </>
          )}
          {route.view === "destinations" && <DestinationsView events={events} loading={eventsLoading} />}
          {route.view === "policies" && <PolicyView selected={route.name} events={events} />}
        </main>
      </div>
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} commands={commands} />
      <ToastHost toasts={toasts} onDismiss={dismiss}
        href={(t) => "/audit?q=" + encodeURIComponent(t.upstream || t.pod)} linkTo={linkTo} />
    </div>
  );
}

render(<App />, document.getElementById("app")!);
