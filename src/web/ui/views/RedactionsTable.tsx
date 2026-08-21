import type { Grouped } from "./types";
import { rowKey } from "./aggregate";

// RedactionsTable is the secrets-redacted ledger (the container computes
// `redactions` via group(events, ["redact"])). Rows are keyboard-operable
// (focus + Enter/Space drills into the pod), matching the fleet table's a11y.
export function RedactionsTable({ redactions, onPod }: { redactions: Grouped[]; onPod: (pod: string) => void }) {
  return (
    <>
      <h2 class="section-title">Secrets redacted</h2>
      {redactions.length === 0
        ? <div class="panel empty">No secrets redacted yet — redact-mode policies strip credentials the agent tries to send.</div>
        : <div class="table-wrap">
            <table>
              <thead><tr><th>pod</th><th>destination</th><th>secrets</th><th>times</th></tr></thead>
              <tbody>
                {redactions.map((c) => (
                  <tr class="clickable" tabIndex={0} onClick={() => onPod(c.pod)} onKeyDown={rowKey(() => onPod(c.pod))}>
                    <td class="c-pod">{c.pod}</td>
                    <td class="c-mono">{c.upstream}</td>
                    <td class="c-mono">{c.secrets}</td>
                    <td class="c-mono">×{c.count}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>}
    </>
  );
}
