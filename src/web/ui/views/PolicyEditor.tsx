import type { ComponentChildren } from "preact";
import { useEffect, useMemo, useState } from "preact/hooks";
import type { AllowRow, Event, Policy, PolicyTemplate, SegOption } from "./types";
import { HTTP_METHODS } from "./types";
import { dryRun, toRows } from "./policy-eval";
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
export function PolicyEditor({ policy, events, scopePods, onSave, onDelete, hint, templates, isDefault, onSetDefault }: {
  policy: Policy; events: Event[]; scopePods: string[];
  onSave: (p: Policy) => Promise<{ ok: boolean; error?: string }>;
  onDelete: () => Promise<void>;
  hint?: (name: string) => ComponentChildren;
  templates?: PolicyTemplate[];
  isDefault?: boolean;
  onSetDefault?: (name: string) => void;
}) {
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

  // Templates offer a starting point on a fresh policy; picking one fills the
  // builder (and the operator can then rename/tweak). Shown only while blank.
  const isNew = !policy.name;
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
    const res = await onSave(draft());
    if (!res.ok) { setErr(res.error || "Save failed"); }
  };
  const del = async () => { await onDelete(); };

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
        {policy.name && onSetDefault && (
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
