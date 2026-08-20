import type { Grouped } from "./types";
import { DecisionBadge } from "./DecisionBadge";

// AttentionPanel lists denied/blocked upstream groups (the container computes
// `attention` via group(events, ["deny", "block"])).
export function AttentionPanel({ attention, onPod }: { attention: Grouped[]; onPod: (pod: string) => void }) {
  return (
    <>
      <h2 class="section-title">Attention</h2>
      {attention.length === 0
        ? <div class="panel empty">No policy denials or blocks — agents are inside their guardrails.</div>
        : <div class="panel">
            {attention.map((a) => (
              <button class="attn" onClick={() => onPod(a.pod)}>
                <span class="attn__pod">{a.pod}</span>
                <span class="attn__desc">
                  <DecisionBadge decision={a.decision} /> {a.upstream}
                </span>
                <span class="attn__count">×{a.count}</span>
              </button>
            ))}
          </div>}
    </>
  );
}
