import type { Verify } from "./types";

export function IntegrityBadge({ v }: { v: Verify }) {
  if (!v) return <span class="badge">verifying…</span>;
  return v.ok
    ? <span class="badge ok">chain intact ✓</span>
    : <span class="badge bad">chain broken @{v.brokenAt} ✗</span>;
}
