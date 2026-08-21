// DecisionBadge renders an audit decision ("allow"/"redact"/"block"/"deny") as
// a small uppercase tag colored by its meaning. Shared by the audit log and
// the overview's Attention panel (previously duplicated inline markup).
export function DecisionBadge({ decision }: { decision?: string }) {
  return (
    <span class={"decision d-" + (decision || "")}>
      {decision || <span class="faint">—</span>}
    </span>
  );
}
