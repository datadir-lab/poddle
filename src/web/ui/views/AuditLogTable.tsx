import { useEffect, useMemo, useState } from "preact/hooks";
import type { Event, SegOption } from "./types";
import { TIME_RANGES, RANGE_MS } from "./types";
import { SegmentedControl } from "./SegmentedControl";
import { DecisionBadge } from "./DecisionBadge";
import { SkelTable } from "./Skeletons";
import { cap1, humanKind, absTime } from "./aggregate";
import { downloadCsv } from "./csv";

const DECISION_FILTER: SegOption[] = [
  { value: "", label: "All" },
  { value: "allow", label: "Allow", tone: "allow" },
  { value: "redact", label: "Redact", tone: "redact" },
  { value: "block", label: "Block", tone: "deny" },
  { value: "deny", label: "Deny", tone: "deny" },
  { value: "monitor", label: "Monitor", tone: "monitor" },
];

// AuditLogTable is fully props-driven (events, initialPod/initialQ) — no
// fetch, router, or api call inside. The container (main.tsx) owns the live
// event source and passes it down. It composes its own toolbar: a text
// filter, a time-range filter, a decision filter with per-decision counts,
// and a CSV export button that downloads the currently-filtered rows.
// `onExport` (optional) fires alongside the download so a consumer can
// observe the export without depending on Blob/URL — the CSV escaping itself
// is covered by e2e.
export function AuditLogTable({ events, initialPod, initialQ, loading, onExport }: {
  events: Event[];
  initialPod?: string;
  initialQ?: string;
  loading: boolean;
  onExport?: (rows: Event[]) => void;
}) {
  const [q, setQ] = useState(initialPod || initialQ || "");
  const [decision, setDecision] = useState("");
  const [range, setRange] = useState("");
  useEffect(() => { if (initialPod) setQ(initialPod); else if (initialQ) setQ(initialQ); }, [initialPod, initialQ]);

  // Narrow by search + time range first; the decision filter is applied last so
  // the per-decision counts reflect everything else the user has narrowed to.
  const matched = useMemo(() => {
    const cutoff = range && RANGE_MS[range] ? Date.now() - RANGE_MS[range] : 0;
    const s = q.toLowerCase();
    return events.filter((e) => {
      if (cutoff && new Date(e.time).getTime() < cutoff) return false;
      if (!q) return true;
      return (e.pod || "").toLowerCase().includes(s) || (e.kind || "").toLowerCase().includes(s) ||
        (e.upstream || "").toLowerCase().includes(s);
    });
  }, [events, q, range]);

  const counts = useMemo(() => {
    const c: Record<string, number> = { "": matched.length, allow: 0, redact: 0, block: 0, deny: 0 };
    for (const e of matched) if (e.decision && e.decision in c) c[e.decision]++;
    return c;
  }, [matched]);
  const shown = useMemo(() => (decision ? matched.filter((e) => e.decision === decision) : matched), [matched, decision]);
  const decisionOpts = DECISION_FILTER.map((o) => ({ ...o, badge: counts[o.value] ?? 0 }));

  // `source` is empty for every event in the single-instance OSS dashboard and
  // populated by the poddle-cloud collector for multi-host fleets — only show
  // (and export) a host column when at least one row actually carries one, so
  // the single-host table/CSV stay byte-identical to today.
  const multiHost = events.some((e) => e.source);

  const exportCsv = () => {
    downloadCsv("poddle-audit.csv", shown, { multiHost });
    onExport?.(shown);
  };

  const toolbar = (
    <div class="toolbar">
      <input class="grow" aria-label="Filter events by pod, kind, or upstream" placeholder="Filter by pod, kind, or upstream…" value={q}
        onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
      <SegmentedControl value={range} options={TIME_RANGES} onChange={setRange} ariaLabel="time range" />
      <SegmentedControl value={decision} options={decisionOpts} onChange={setDecision} ariaLabel="filter by decision" />
      <button type="button" class="btn btn--ghost btn--sm" disabled={!shown.length} onClick={exportCsv}>Export CSV</button>
      <span class="count">{shown.length} events</span>
    </div>
  );

  if (loading) return <div>{toolbar}<SkelTable rows={8} /></div>;

  return (
    <div>
      {toolbar}
      <div class="table-wrap">
        <table class="dense">
          <thead>
            <tr>
              {multiHost && <th scope="col">host</th>}
              <th scope="col">time</th><th scope="col">pod</th><th scope="col">kind</th><th scope="col">decision</th><th scope="col">upstream</th><th scope="col">detail</th>
            </tr>
          </thead>
          <tbody>
            {shown.length === 0 && (
              <tr><td colSpan={multiHost ? 7 : 6} class="empty">
                {q || decision || range ? "No events match your filter." : "Monitoring active — no events recorded yet."}
              </td></tr>
            )}
            {shown.slice(0, 800).map((e) => (
              <tr key={e.seq} class="auditrow">
                {multiHost && <td class="c-host">{e.source || <span class="faint">—</span>}</td>}
                <td class="c-time" title={new Date(e.time).toLocaleString()}>{absTime(e.time)}</td>
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
