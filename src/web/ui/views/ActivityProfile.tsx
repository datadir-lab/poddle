import { useMemo } from "preact/hooks";
import type { Event, Policy } from "./types";
import { categorize, suggestPolicy } from "./activity";
import { DecisionBadge } from "./DecisionBadge";

// ActivityProfile renders one pod's egress categorized ("what it accesses"), with
// the verb layer where visible, plus blocked attempts, and a button that hands a
// least-privilege policy suggestion up to the shell to open in the editor.
export function ActivityProfile({ podName, events, onSuggestPolicy }: {
  podName: string;
  events: Event[];
  onSuggestPolicy: (p: Policy) => void;
}) {
  const rolls = useMemo(() => categorize(events), [events]);
  const blocked = useMemo(
    () => rolls.filter((r) => r.deny + r.block > 0),
    [rolls],
  );
  if (rolls.length === 0) {
    return (
      <section class="activity">
        <h2 class="detail-sub">Activity</h2>
        <p class="rules__empty">No egress recorded for this pod yet.</p>
      </section>
    );
  }
  return (
    <section class="activity">
      <div class="activity__head">
        <h2 class="detail-sub">Activity</h2>
        <button type="button" class="btn" onClick={() => onSuggestPolicy(suggestPolicy(events, podName + "-policy"))}>
          Create policy from this pod
        </button>
      </div>
      <ul class="activity__cats">
        {rolls.map((r) => (
          <li class="activity__cat" key={r.key}>
            <span class="activity__label">{r.label}</span>
            <span class="activity__hosts c-mono">{r.hosts.join(", ")}</span>
            <span class="activity__methods c-mono">{r.methods.length ? r.methods.join(" ") : "— tunnelled"}</span>
            <span class="activity__chips">
              {r.allow > 0 && <DecisionBadge decision="allow" />}
              {r.redact > 0 && <DecisionBadge decision="redact" />}
              {r.deny > 0 && <DecisionBadge decision="deny" />}
              {r.block > 0 && <DecisionBadge decision="block" />}
            </span>
            <span class="activity__n">{r.total}</span>
          </li>
        ))}
      </ul>
      {blocked.length > 0 && (
        <p class="activity__note">Blocked attempts are shown above but are not added to the suggested policy.</p>
      )}
    </section>
  );
}
