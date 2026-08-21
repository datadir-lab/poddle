import { useState } from "preact/hooks";
import type { Pending, Pod, Policy } from "./types";

// PodControls are the mutating actions on a live pod, both confirmed inline:
// rebind its governing policy and revoke its credentials. It is presentational —
// the container injects the mutations. `onBind(policyName)` binds the named
// policy and returns { ok, msg }; `onRevoke()` revokes every credential and
// returns { ok, msg }. Both messages are rendered verbatim in the status line,
// so the container owns the exact copy. `policies` renders the selectable
// chips; `pod` supplies the current binding + the confirm-copy pod name.
export function PodControls({ pod, policies, onBind, onRevoke }: {
  pod: Pod; policies: Policy[];
  onBind: (policyName: string) => Promise<{ ok: boolean; msg: string }>;
  onRevoke: () => Promise<{ ok: boolean; msg: string }>;
}) {
  const [pending, setPending] = useState<Pending>(null);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<{ ok: boolean; msg: string } | null>(null);

  const bind = async (name: string) => {
    setBusy(true);
    const res = await onBind(name);
    setBusy(false); setPending(null);
    setStatus(res);
  };
  const revoke = async () => {
    setBusy(true);
    const res = await onRevoke();
    setBusy(false); setPending(null);
    setStatus(res);
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
