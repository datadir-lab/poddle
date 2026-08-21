import { Icon } from "./Icon";

// StatCard is a single headline number (e.g. "pods active", "requests"). An
// optional `icon` (an Icon name) and `tone` (e.g. "warn"/"flag") let a card
// carry meaning beyond the number.
export function StatCard({ n, label, icon, tone }: { n: number | string; label: string; icon?: string; tone?: string }) {
  return (
    <div class={"card" + (tone ? " card--" + tone : "")}>
      {icon && <span class="card__icon" aria-hidden="true"><Icon name={icon} size={17} /></span>}
      <div class="card__num">{n}</div>
      <div class="card__label">{label}</div>
    </div>
  );
}
