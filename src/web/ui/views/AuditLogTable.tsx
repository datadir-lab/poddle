import { useEffect, useMemo, useState } from "preact/hooks";
import type { Event, SegOption } from "./types";
import { SegmentedControl } from "./SegmentedControl";
import { DecisionBadge } from "./DecisionBadge";
import { cap1, humanKind, relTime } from "./aggregate";

const DECISION_FILTER: SegOption[] = [
  { value: "", label: "All" },
  { value: "allow", label: "Allow", tone: "allow" },
  { value: "redact", label: "Redact", tone: "redact" },
  { value: "block", label: "Block", tone: "deny" },
  { value: "deny", label: "Deny", tone: "deny" },
];

// AuditLogTable is fully props-driven (events, initialPod) — no fetch inside.
// The container (main.tsx) owns the live event source and passes it down.
export function AuditLogTable({ events, initialPod }: { events: Event[]; initialPod?: string }) {
  const [q, setQ] = useState(initialPod || "");
  const [decision, setDecision] = useState("");
  useEffect(() => { if (initialPod) setQ(initialPod); }, [initialPod]);

  const shown = useMemo(() => events.filter((e) => {
    if (decision && e.decision !== decision) return false;
    if (!q) return true;
    const s = q.toLowerCase();
    return (e.pod || "").toLowerCase().includes(s) || (e.kind || "").toLowerCase().includes(s) ||
      (e.upstream || "").toLowerCase().includes(s);
  }), [events, q, decision]);

  return (
    <div>
      <div class="toolbar">
        <input class="grow" aria-label="Filter events by pod, kind, or upstream" placeholder="Filter by pod, kind, or upstream…" value={q}
          onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
        <SegmentedControl value={decision} options={DECISION_FILTER} onChange={setDecision} ariaLabel="filter by decision" />
        <span class="count">{shown.length} events</span>
      </div>
      <div class="table-wrap">
        <table class="dense">
          <thead>
            <tr><th scope="col">time</th><th scope="col">pod</th><th scope="col">kind</th><th scope="col">decision</th><th scope="col">upstream</th><th scope="col">detail</th></tr>
          </thead>
          <tbody>
            {shown.length === 0 && (
              <tr><td colSpan={6} class="empty">
                {q || decision ? "No events match your filter." : "Monitoring active — no events recorded yet."}
              </td></tr>
            )}
            {shown.slice(0, 800).map((e) => (
              <tr key={e.seq}>
                <td class="c-time" title={new Date(e.time).toLocaleString()}>{relTime(e.time)}</td>
                <td class="c-pod">{e.pod || <span class="faint">—</span>}</td>
                <td>{humanKind(e.kind)}</td>
                <td><DecisionBadge decision={e.decision} /></td>
                <td class="c-mono">{e.upstream || <span class="faint">—</span>}</td>
                <td class="c-detail">{cap1(e.detail || "")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
