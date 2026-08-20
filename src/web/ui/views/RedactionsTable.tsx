import type { Grouped } from "./types";

// RedactionsTable is the secrets-redacted ledger (the container computes
// `redactions` via group(events, ["redact"])).
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
                  <tr onClick={() => onPod(c.pod)} class="clickable">
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
