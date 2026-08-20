import type { ComponentChildren } from "preact";
import { useEffect, useState } from "preact/hooks";
import type { Policy, SegOption } from "./types";
import { SegmentedControl } from "./SegmentedControl";

const lines = (a?: string[]) => (a || []).join("\n");
const parseLines = (s: string) => s.split("\n").map((x) => x.trim()).filter(Boolean);

const EGRESS_MODES: SegOption[] = [
  { value: "redact", label: "Redact", tone: "redact" },
  { value: "block", label: "Block", tone: "deny" },
  { value: "off", label: "Off", tone: "faint" },
];

// PolicyEditor is the pure render half of the policy editor form: it owns only
// the field state and client-side validation. Persistence is injected —
// `onSave`/`onDelete` are plain semantic callbacks, so no fetch()/api./v1 code
// lives in this file. That keeps this component usable by both the AGPL core
// dashboard (real /v1 API) and any other consumer with different plumbing.
// `hint` lets the caller supply consumer-specific reference copy (e.g. the
// core dashboard's `poddle up --policy` CLI hint) without hardcoding it here.
export function PolicyEditor({ policy, onSave, onDelete, hint }: {
  policy: Policy;
  onSave: (p: Policy) => Promise<{ ok: boolean; error?: string }>;
  onDelete: () => Promise<void>;
  hint?: ComponentChildren;
}) {
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
    const res = await onSave({
      name: name.trim(), allow_upstreams: parseLines(allow), deny_upstreams: parseLines(deny),
      methods: parsedMethods, egress,
    });
    if (!res.ok) { setErr(res.error || "save failed"); return; }
  };
  const del = async () => { await onDelete(); };

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
      {hint && <div class="hint">{hint}</div>}
    </div>
  );
}
