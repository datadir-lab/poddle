import { render } from "preact";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import "@fontsource-variable/inter";
import "@fontsource-variable/fraunces/full.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource/jetbrains-mono/600.css";
import "./style.css";
import type { Stats } from "@poddle/ui/views";
import {
  SegmentedControl, DecisionBadge, IntegrityBadge, IntegrityPanel,
  Icon, PoddleMark, EgressChart, PostureBar, FleetLoad,
  SkelCards, SkelTable, LiveDot,
  OverviewCards, AttentionPanel, RedactionsTable, PodFleetTable, PodDetailPanel,
  AuditLogTable, PolicyList, DestinationsTable,
  summarise, group, decisionCounts, destinations,
  TIME_RANGES, RANGE_MS,
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
// Segment options for the various filter/mode controls in this file (rendered
// via the imported SegmentedControl). An option's `tone` colors the active
// segment by its meaning (e.g. block = deny-red); `badge` renders a count.
type SegOption = { value: string; label: string; tone?: string; badge?: string | number };

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

const HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
// A single allow-list row in the visual builder: a host plus an optional set of
// methods it is restricted to (empty = any method). `open` reveals the method
// toggles even before any are picked.
type AllowRow = { host: string; methods: string[]; open: boolean };

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

// toRows expands a stored policy into builder rows (union of the allow-list and
// any hosts that carry method restrictions, so nothing is lost on a round-trip).
function toRows(p: Policy): AllowRow[] {
  const m = p.methods || {};
  const hosts = [...new Set([...(p.allow_upstreams || []), ...Object.keys(m)])];
  return hosts.map((h) => ({ host: h, methods: m[h] || [], open: false }));
}

function PolicyEditor({ policy, events, scopePods, onSaved, onDeleted }: { policy: Policy; events: Event[]; scopePods: string[]; onSaved: (name: string) => void; onDeleted: () => void }) {
  const [name, setName] = useState(policy.name);
  const [allows, setAllows] = useState<AllowRow[]>(() => toRows(policy));
  const [denies, setDenies] = useState<string[]>(policy.deny_upstreams || []);
  const [egress, setEgress] = useState(policy.egress || "redact");
  const [err, setErr] = useState("");

  useEffect(() => {
    setName(policy.name); setAllows(toRows(policy)); setDenies(policy.deny_upstreams || []);
    setEgress(policy.egress || "redact"); setErr("");
  }, [policy]);

  const patchAllow = (i: number, patch: Partial<AllowRow>) => setAllows((a) => a.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const toggleMethod = (i: number, m: string) => setAllows((a) => a.map((r, j) => j === i ? { ...r, methods: r.methods.includes(m) ? r.methods.filter((x) => x !== m) : [...r.methods, m] } : r));
  const addAllow = () => setAllows((a) => [...a, { host: "", methods: [], open: false }]);
  const removeAllow = (i: number) => setAllows((a) => a.filter((_, j) => j !== i));
  const patchDeny = (i: number, v: string) => setDenies((d) => d.map((x, j) => (j === i ? v : x)));
  const addDeny = () => setDenies((d) => [...d, ""]);
  const removeDeny = (i: number) => setDenies((d) => d.filter((_, j) => j !== i));

  // Assemble the (unsaved) policy from the builder rows — shared by save + dry-run.
  const draft = (): Policy => {
    const allow_upstreams = allows.map((r) => r.host.trim()).filter(Boolean);
    const deny_upstreams = denies.map((d) => d.trim()).filter(Boolean);
    const methods: Record<string, string[]> = {};
    for (const r of allows) { const h = r.host.trim(); if (h && r.methods.length) methods[h] = r.methods; }
    return { name: name.trim(), allow_upstreams, deny_upstreams, methods, egress };
  };

  // Live dry-run against recent traffic, recomputed as the rules change.
  // Scope the dry-run to the traffic of the pods that run this policy. With none
  // (a new or unused policy) there is nothing to scope to, so preview against all
  // recent egress instead — and say so.
  const scoped = scopePods.length > 0;
  const dryEvents = useMemo(
    () => (scoped ? events.filter((e) => e.pod && scopePods.includes(e.pod)) : events),
    [events, scopePods, scoped],
  );
  const impact = useMemo(() => dryRun(draft(), dryEvents), [name, allows, denies, egress, dryEvents]);

  const save = async () => {
    if (!name.trim()) { setErr("Name is required."); return; }
    const res = await api.putPolicy(draft());
    if (!res.ok) { setErr("Save failed: " + res.status); return; }
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

      <label>Allowed destinations <span class="label-hint">Default-deny once any are set · ".example.com" matches any subdomain</span></label>
      <div class="rules">
        {allows.length === 0 && <p class="rules__empty">No destinations yet — every host is allowed, subject to the blocked list and egress mode.</p>}
        {allows.map((r, i) => (
          <div class="rule" key={i}>
            <div class="rule__row">
              <input class="rule__host" value={r.host} placeholder="api.example.com" aria-label="Allowed host"
                onInput={(e) => patchAllow(i, { host: (e.target as HTMLInputElement).value })} />
              {!r.open && (r.methods.length
                ? <button type="button" class="rule__msum" title={"Limited to " + r.methods.join(", ") + " — click to edit"} onClick={() => patchAllow(i, { open: true })}>{r.methods.length > 3 ? r.methods.length + " methods" : r.methods.join(", ")}</button>
                : <button type="button" class="rule__limit" onClick={() => patchAllow(i, { open: true })}>＋ limit methods</button>)}
              <button type="button" class="rule__rm" aria-label="Remove destination" onClick={() => removeAllow(i)}>×</button>
            </div>
            {r.open && (
              <div class="rule__methods">
                <span class="rule__mlabel">Allow only:</span>
                {HTTP_METHODS.map((m) => (
                  <button type="button" key={m} class={"mchip" + (r.methods.includes(m) ? " on" : "")} aria-pressed={r.methods.includes(m)} onClick={() => toggleMethod(i, m)}>{m}</button>
                ))}
                <button type="button" class="rule__mdone" onClick={() => patchAllow(i, { open: false })}>Done</button>
                {r.methods.length > 0 && <button type="button" class="rule__mclear" onClick={() => patchAllow(i, { methods: [], open: false })}>Clear</button>}
              </div>
            )}
          </div>
        ))}
        <button type="button" class="addrow" onClick={addAllow}>＋ Add destination</button>
      </div>

      <label>Always blocked <span class="label-hint">Wins over the allow-list</span></label>
      <div class="rules">
        {denies.map((h, i) => (
          <div class="rule" key={i}>
            <div class="rule__row">
              <input class="rule__host" value={h} placeholder="metadata.google.internal" aria-label="Blocked host"
                onInput={(e) => patchDeny(i, (e.target as HTMLInputElement).value)} />
              <button type="button" class="rule__rm" aria-label="Remove blocked host" onClick={() => removeDeny(i)}>×</button>
            </div>
          </div>
        ))}
        <button type="button" class="addrow" onClick={addDeny}>＋ Add blocked host</button>
      </div>

      <div class="dryrun">
        <div class="dryrun__head">
          <span class="dryrun__title">Dry-run · {scoped ? `${scopePods.length} pod${scopePods.length === 1 ? "" : "s"} on this policy` : "all recent egress"}</span>
          <span class="dryrun__stat">
            {impact.total} request{impact.total === 1 ? "" : "s"} ·{" "}
            <span class={impact.denied ? "dryrun__deny" : "dryrun__ok"}>{impact.denied} would be denied</span>
          </span>
        </div>
        {impact.total === 0
          ? <div class="dryrun__empty">{scoped ? "The pods on this policy have no recent egress to evaluate." : "No recent egress to evaluate yet."}</div>
          : impact.denied === 0
            ? <div class="dryrun__pass"><Icon name="check" size={14} /> Every request passes these rules.</div>
            : <ul class="dryrun__list">
                {impact.rows.slice(0, 8).map((r) => (
                  <li key={r.method + r.upstream}>
                    <DecisionBadge decision="deny" />
                    <span class="c-mono dryrun__dest">{r.method ? r.method + " " : ""}{r.upstream}</span>
                    <span class="dryrun__reason">{r.reason}</span>
                    <span class="dryrun__n">×{r.count}</span>
                  </li>
                ))}
                {impact.rows.length > 8 && <li class="dryrun__more">+{impact.rows.length - 8} more destinations</li>}
              </ul>}
        <p class="dryrun__note">
          {scoped
            ? "Replays these rules over the recent requests made by the pods that run this policy."
            : "No pods run this policy yet — previewed against all recent egress."}{" "}
          Evaluates allow/deny and method rules; secret redaction depends on request contents and is not simulated.
        </p>
      </div>

      {err && <div class="err">{err}</div>}
      <div class="actions">
        <button class="btn btn--primary" onClick={save}>Save</button>
        {policy.name && <button class="btn btn--danger" onClick={del}>Delete</button>}
      </div>
      <div class="hint">Reference from a pod: <code>poddle up --policy {name || "<name>"}</code>, or <code>policy = "{name || "<name>"}"</code> in a template.</div>
    </div>
  );
}

function PolicyView({ selected, events }: { selected?: string; events: Event[] }) {
  const [policies, setPolicies] = useState<Policy[]>([]);
  const [loading, setLoading] = useState(true);
  const { pods } = usePods();
  const load = () => api.policies().then((ps) => setPolicies(asArray<Policy>(ps))).catch(() => setPolicies([])).finally(() => setLoading(false));
  useEffect(() => { load(); }, []);

  // Fleet governance: how many running pods each policy governs, and which run
  // with none (a real risk — an unpoliced pod's egress is unrestricted).
  const running = pods.filter((p) => p.state === "running");
  const usage = (name: string) => running.filter((p) => p.policy === name).length;
  const ungoverned = running.filter((p) => !p.policy);
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
    () => selected === "new" ? { name: "", egress: "redact" }
      : selected ? (policies.find((p) => p.name === selected) || null)
        : null,
    [selected, policies],
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
          </span>
        </div>
      )}
      <div class="layout">
        <PolicyList policies={policies} selectedName={selected} loading={loading} usage={usage}
          hrefFor={hrefFor} newHref="/policies/new" linkTo={linkTo} />
        {sel
          ? <PolicyEditor policy={sel} events={events} scopePods={usingPods}
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
        <IntegrityBadge v={v} compact={collapsed} href="/audit" onClick={linkTo("/audit")} />
        <ThemeToggle />
      </div>
    </aside>
  );
}

// goPod routes to a pod's drill-down page.
const goPod = (pod: string) => navigate("/pods/" + encodeURIComponent(pod));

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
  useEffect(() => { api.policies().then((ps) => setPolicies(asArray<Policy>(ps))).catch(() => {}); }, []);
  const pod = pods.find((p) => p.name === name);
  const h = hist[name] || { cpu: [], mem: [] };
  const controls = pod && pod.state === "running" ? <PodControls pod={pod} policies={policies} /> : undefined;
  const policyHref = pod?.policy ? `/policies/${encodeURIComponent(pod.policy)}` : undefined;
  return (
    <PodDetailPanel name={name} pod={pod} hist={h} events={events} loading={loading}
      backHref="/pods" onBack={linkTo("/pods")}
      policyHref={policyHref} onPolicyClick={policyHref ? linkTo(policyHref) : undefined}
      controls={controls} />
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
    api.pods().then((p) => setPods(asArray<Pod>(p))).catch(() => {});
    api.policies().then((p) => setPols(asArray<Policy>(p))).catch(() => {});
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
              <div class="toast__title"><span class="c-pod">{t.pod}</span> <DecisionBadge decision={t.decision} /></div>
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
              <AuditLogTable events={events} initialPod={route.pod} initialQ={route.q} loading={eventsLoading} />
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
