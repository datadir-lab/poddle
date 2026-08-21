import type { Verify } from "./types";
import { Icon } from "./Icon";
import { absTime } from "./aggregate";

// IntegrityPanel is the provenance centerpiece of the Audit view: it states the
// hash-chain verdict in plain language, shows when it was last checked, and lets
// the operator re-verify on demand. A broken chain names the first bad seq.
export function IntegrityPanel({ verify, checkedAt, recheck, count }: { verify: Verify; checkedAt: number; recheck: () => void; count: number }) {
  const state = verify == null ? "verifying" : verify.ok ? "intact" : "broken";
  const headline = state === "verifying" ? "Verifying chain…"
    : state === "intact" ? "Audit chain intact"
      : `Chain broken at #${verify!.brokenAt}`;
  return (
    <div class={"integrity integrity--" + state}>
      <span class="integrity__icon" aria-hidden="true"><Icon name={state === "broken" ? "octagon" : "policies"} size={22} /></span>
      <div class="integrity__body">
        <div class="integrity__status">{headline}</div>
        <p class="integrity__desc">
          {state === "broken"
            ? "An event was altered or removed after it was written — everything from that point on is suspect."
            : "Every event is hash-linked to the one before it, so any edit or deletion is detectable after the fact."}
        </p>
      </div>
      <dl class="integrity__meta">
        <div><dt>Events</dt><dd>{count}</dd></div>
        <div><dt>Last verified</dt><dd>{checkedAt ? absTime(new Date(checkedAt).toISOString()) : "…"}</dd></div>
      </dl>
      <button type="button" class="btn btn--ghost btn--sm integrity__btn" onClick={recheck}>Re-verify</button>
    </div>
  );
}
