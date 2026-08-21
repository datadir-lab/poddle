import type { Verify } from "./types";
import { Icon } from "./Icon";

// IntegrityBadge is the at-a-glance provenance signal. It links to the audit
// view, where the integrity panel explains the hash-chain in full; the tooltip
// gives the short version on hover. Renamed from the dashboard's VerifyBadge.
//
// This component only renders an <a href>: the consumer (the dashboard's
// router, or the cloud console's) supplies both `href` (so the link is a real,
// deep-linkable URL) and `onClick` (so the consumer can intercept a plain left-
// click for SPA navigation while a modifier-click still opens a new tab) — no
// navigate()/linkTo() logic lives in this package.
export function IntegrityBadge({ v, compact, href, onClick }: {
  v: Verify; compact?: boolean; href?: string; onClick?: (e: MouseEvent) => void;
}) {
  const cls = v == null ? "badge" : v.ok ? "badge ok" : "badge bad";
  const label = v == null ? "Verifying…" : v.ok ? "Chain intact ✓" : `Chain broken @${v.brokenAt} ✗`;
  const tip = v == null
    ? "Checking the audit hash-chain…"
    : v.ok
      ? "Every audit event is hash-linked to the one before it, so any edit or deletion is detectable. Intact means nothing was tampered with. Click to open the audit trail."
      : `The audit hash-chain is broken at event #${v.brokenAt}: a row was altered or removed. Click to open the audit trail.`;
  // aria-label pins the accessible name (the CSS tooltip's ::after text would
  // otherwise fold into it); the visual tooltip carries the fuller explanation.
  if (compact) {
    return (
      <a class={cls + " badge--icon"} href={href} title={label} aria-label={label} onClick={onClick}>
        <Icon name={v && !v.ok ? "octagon" : "policies"} size={15} />
      </a>
    );
  }
  return <a class={cls} href={href} data-tip={tip} aria-label={label} onClick={onClick}>{label}</a>;
}
