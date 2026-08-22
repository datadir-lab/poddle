import type { ComponentChildren } from "preact";
import { useEffect, useMemo, useState } from "preact/hooks";
import type { AllowRow, Event, Policy, PolicyTemplate, SegOption } from "./types";
import { HTTP_METHODS } from "./types";
import { decide, dryRun, matchHost, toRows } from "./policy-eval";
import { SegmentedControl } from "./SegmentedControl";
import { Icon } from "./Icon";
import { DecisionBadge } from "./DecisionBadge";

// The egress-mode options for the visual builder's segmented control. Moved with
// the editor from src/web/dashboard/src/main.tsx so no dashboard import remains.
const EGRESS_MODES: SegOption[] = [
  { value: "redact", label: "Redact", tone: "redact" },
  { value: "block", label: "Block", tone: "deny" },
  { value: "off", label: "Off", tone: "faint" },
];

// Enforcement is a separate axis from egress (which governs secrets): "monitor"
// evaluates the access rules but forwards would-be denials, logging them, for a
// safe rollout before switching to "enforce".
const ENFORCEMENT_MODES: SegOption[] = [
  { value: "enforce", label: "Enforce" },
  { value: "monitor", label: "Monitor", tone: "monitor" },
];

// The cloud metadata endpoints — a top credential-theft target; the "Block them"
// advisory fix adds these to the deny-list.
const METADATA_HOSTS = ["169.254.169.254", "metadata.google.internal"];

// lintPolicy inspects a (draft) policy for common misconfigurations — the
// editor's governance coach. Access-control reasoning only, using the same
// matchHost semantics the engine enforces.
type Advisory = { level: "warn" | "info"; msg: string; fix?: "block-metadata" };
function lintPolicy(p: Policy): Advisory[] {
  const out: Advisory[] = [];
  const allow = p.allow_upstreams || [];
  const deny = p.deny_upstreams || [];
  const methods = p.methods || {};
  const allowAll = allow.length === 0;

  if (allowAll && p.egress !== "block") {
    out.push({ level: "warn", msg: "No allowed destinations, so every host is reachable. Add destinations, or set egress to Block." });
  }
  const metaOpen = METADATA_HOSTS.some((h) => !matchHost(h, deny) && (allowAll || matchHost(h, allow)));
  if (metaOpen) {
    out.push({ level: "warn", msg: "Cloud metadata endpoints are reachable — a common credential-theft target.", fix: "block-metadata" });
  }
  if (!allowAll) {
    for (const h of Object.keys(methods)) {
      if (!h.startsWith(".") && !matchHost(h, allow)) {
        out.push({ level: "info", msg: `Method rule on "${h}" has no effect — it is not in the allowed destinations.` });
      }
    }
  }
  for (const a of allow) {
    if (deny.includes(a)) out.push({ level: "info", msg: `"${a}" is both allowed and blocked — blocked wins, so it is effectively blocked.` });
  }
  if (p.egress === "off") {
    out.push({ level: "warn", msg: "Secret scanning is off — outbound secrets are sent as-is, not redacted." });
  }
  return out;
}

// PolicyEditor is the visual policy builder: name, egress mode, an allow-list of
// destinations (each optionally restricted to a set of HTTP methods), a
// deny-list, and a live dry-run against recent traffic. It is presentational —
// the container injects the mutations. `onSave` persists the built policy and
// returns { ok, error } (the container owns reload + routing on success, so
// this component never changes the URL); `onDelete` removes it. `events`/`scopePods`
// feed the dry-run (scoped to the pods that run this policy, when any). `hint`
// is an optional override for the CLI-reference footer — it receives the live
// name so a consumer can render its own reference copy; the default is poddle's
// own `poddle up --policy …` hint. `templates` (when supplied) offer one-click
// starting points on a fresh, still-blank policy; `isDefault`/`onSetDefault`
// let the container mark this policy as the fleet default — all injected, so no
// template set or default-policy transport lives in this file.
export function PolicyEditor({ policy, events, scopePods, onSave, onDelete, hint, templates, isSaved, isDefault, onSetDefault, onDuplicate }: {
  policy: Policy; events: Event[]; scopePods: string[];
  onSave: (p: Policy) => Promise<{ ok: boolean; error?: string }>;
  onDelete: () => Promise<void>;
  hint?: (name: string) => ComponentChildren;
  templates?: PolicyTemplate[];
  isSaved?: boolean;
  isDefault?: boolean;
  onSetDefault?: (name: string) => void;
  onDuplicate?: (p: Policy) => void;
}) {
  const [name, setName] = useState(policy.name);
  const [desc, setDesc] = useState(policy.description || "");
  const [allows, setAllows] = useState<AllowRow[]>(() => toRows(policy));
  const [denies, setDenies] = useState<string[]>(policy.deny_upstreams || []);
  const [egress, setEgress] = useState(policy.egress || "redact");
  const [monitor, setMonitor] = useState(!!policy.monitor);
  const [err, setErr] = useState("");
  const [probeHost, setProbeHost] = useState("");
  const [probeMethod, setProbeMethod] = useState("GET");

  useEffect(() => {
    setName(policy.name); setDesc(policy.description || ""); setAllows(toRows(policy)); setDenies(policy.deny_upstreams || []);
    setEgress(policy.egress || "redact"); setMonitor(!!policy.monitor); setErr("");
  }, [policy]);

  const patchAllow = (i: number, patch: Partial<AllowRow>) => setAllows((a) => a.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const toggleMethod = (i: number, m: string) => setAllows((a) => a.map((r, j) => j === i ? { ...r, methods: r.methods.includes(m) ? r.methods.filter((x) => x !== m) : [...r.methods, m] } : r));
  const addAllow = () => setAllows((a) => [...a, { host: "", methods: [], open: false }]);
  const removeAllow = (i: number) => setAllows((a) => a.filter((_, j) => j !== i));
  const patchDeny = (i: number, v: string) => setDenies((d) => d.map((x, j) => (j === i ? v : x)));
  const addDeny = () => setDenies((d) => [...d, ""]);
  const removeDeny = (i: number) => setDenies((d) => d.filter((_, j) => j !== i));

  // Whether this is a persisted policy. The container passes isSaved (a "new"
  // policy can carry a name via a Duplicate seed); fall back to the name for
  // standalone use of the component.
  const saved = isSaved ?? !!policy.name;
  // Templates offer a starting point on a fresh policy; picking one fills the
  // builder (and the operator can then rename/tweak). Shown only while blank.
  const isNew = !saved;
  const blank = !name && allows.length === 0 && denies.length === 0;
  const applyTemplate = (t: PolicyTemplate) => {
    setName(t.id);
    setAllows(toRows({ name: t.id, ...t.policy }));
    setDenies(t.policy.deny_upstreams || []);
    setEgress(t.policy.egress || "redact");
    setErr("");
  };

  // Assemble the (unsaved) policy from the builder rows — shared by save + dry-run.
  const draft = (): Policy => {
    const allow_upstreams = allows.map((r) => r.host.trim()).filter(Boolean);
    const deny_upstreams = denies.map((d) => d.trim()).filter(Boolean);
    const methods: Record<string, string[]> = {};
    for (const r of allows) { const h = r.host.trim(); if (h && r.methods.length) methods[h] = r.methods; }
    return { name: name.trim(), description: desc.trim() || undefined, allow_upstreams, deny_upstreams, methods, egress, monitor: monitor || undefined };
  };

  // Governance advisories on the live draft, and a single-request probe that runs
  // the same client engine as the dry-run — both recompute as the rules change.
  const advisories = useMemo(() => lintPolicy(draft()), [allows, denies, egress]);
  const probe = useMemo(() => (probeHost.trim() ? decide(draft(), probeHost.trim(), probeMethod) : null), [probeHost, probeMethod, allows, denies, egress]);
  const blockMetadata = () => setDenies((d) => [...new Set([...d.map((x) => x.trim()).filter(Boolean), ...METADATA_HOSTS])]);

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
  // Real "monitor" decisions logged for this policy's pods while it runs in
  // monitor mode — the safe-rollout signal (0 over a window with traffic = ready
  // to enforce).
  const monitorHits = useMemo(() => dryEvents.filter((e) => e.decision === "monitor").length, [dryEvents]);

  const save = async () => {
    if (!name.trim()) { setErr("Name is required."); return; }
    const res = await onSave(draft());
    if (!res.ok) { setErr(res.error || "Save failed"); }
  };
  const del = async () => { await onDelete(); };
  // Promote a monitored policy to enforcement in one click (save with monitor off).
  const enforceNow = async () => {
    const res = await onSave({ ...draft(), monitor: undefined });
    if (res.ok) setMonitor(false); else setErr(res.error || "Save failed");
  };

  return (
    <div class="editor">
      {isNew && blank && templates && templates.length > 0 && (
        <div class="templates">
          <div class="templates__label">Start from a template</div>
          <div class="templates__grid">
            {templates.map((t) => (
              <button type="button" key={t.id} class="tmpl" onClick={() => applyTemplate(t)}>
                <span class="tmpl__name">{t.label}</span>
                <span class="tmpl__hint">{t.hint}</span>
              </button>
            ))}
          </div>
          <div class="templates__or">or build one from scratch below</div>
        </div>
      )}
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

      <label for="pol-desc">Description <span class="label-hint">Optional — what this policy is for</span></label>
      <input id="pol-desc" value={desc} placeholder="e.g. CI agents: model + package registries" onInput={(e) => setDesc((e.target as HTMLInputElement).value)} />

      <label>Enforcement <span class="label-hint">Monitor logs would-be denials without blocking — roll out safely, then Enforce</span></label>
      <SegmentedControl value={monitor ? "monitor" : "enforce"} options={ENFORCEMENT_MODES} onChange={(v) => setMonitor(v === "monitor")} ariaLabel="enforcement mode" />

      {!blank && advisories.length > 0 && (
        <div class="advisories">
          {advisories.map((a, i) => (
            <div class={"advisory advisory--" + a.level} key={i}>
              <span class="advisory__icon" aria-hidden="true"><Icon name={a.level === "warn" ? "ban" : "info"} size={14} /></span>
              <span class="advisory__msg">{a.msg}</span>
              {a.fix === "block-metadata" && <button type="button" class="advisory__fix" onClick={blockMetadata}>Block them</button>}
            </div>
          ))}
        </div>
      )}

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
            <span class={impact.denied ? "dryrun__deny" : "dryrun__ok"}>{impact.denied} would be {monitor ? "logged" : "denied"}</span>
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

      {isSaved && policy.monitor && (
        <div class={"rollout " + (impact.total === 0 ? "rollout--idle" : monitorHits > 0 ? "rollout--warn" : "rollout--clear")}>
          <span class="rollout__ic" aria-hidden="true"><Icon name={impact.total === 0 ? "info" : monitorHits > 0 ? "ban" : "check"} size={15} /></span>
          <span class="rollout__msg">
            {impact.total === 0
              ? <>Monitoring — no recent traffic from this policy's pods to evaluate yet.</>
              : monitorHits > 0
                ? <><strong>{monitorHits}</strong> request{monitorHits === 1 ? "" : "s"} would have been denied while monitoring. Review in Audit (filter Monitor) before enforcing.</>
                : <>No would-be denials logged for this policy's pods recently — safe to enforce.</>}
          </span>
          {impact.total > 0 && monitorHits === 0 && (
            <button type="button" class="rollout__enforce" onClick={enforceNow}>Enforce now</button>
          )}
        </div>
      )}

      <div class="probe">
        <div class="probe__label">Test a request</div>
        <div class="probe__row">
          <input class="probe__host" value={probeHost} placeholder="host, e.g. api.github.com" aria-label="Test request host"
            onInput={(e) => setProbeHost((e.target as HTMLInputElement).value)} />
          <SegmentedControl value={probeMethod} options={HTTP_METHODS.map((m) => ({ value: m, label: m }))} onChange={setProbeMethod} ariaLabel="test request method" />
        </div>
        {probe && (
          <div class={"probe__result " + (probe.allow ? "ok" : "bad")} role="status">
            <DecisionBadge decision={probe.allow ? "allow" : "deny"} />
            <span class="probe__reason">{probe.allow ? "This request would be allowed." : probe.reason}</span>
          </div>
        )}
      </div>

      {err && <div class="err">{err}</div>}
      <div class="actions">
        <button class="btn btn--primary" onClick={save}>Save</button>
        {saved && <button class="btn btn--danger" onClick={del}>Delete</button>}
        {saved && onDuplicate && <button type="button" class="btn btn--ghost" onClick={() => onDuplicate(draft())}>Duplicate</button>}
        {saved && onSetDefault && (
          <button type="button" class={"btn btn--ghost btn--default" + (isDefault ? " is-default" : "")}
            title={isDefault
              ? "This policy is applied to pods started with no --policy. Click to clear."
              : "Apply this policy to any pod started with no --policy."}
            onClick={() => onSetDefault(isDefault ? "" : policy.name)}>
            {isDefault ? "★ Default" : "Set as default"}
          </button>
        )}
      </div>
      {hint
        ? hint(name)
        : <div class="hint">Reference from a pod: <code>poddle up --policy {name || "<name>"}</code>, or <code>policy = "{name || "<name>"}"</code> in a template.</div>}
    </div>
  );
}
