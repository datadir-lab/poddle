export function StatCard({ n, label, tone }: { n: number | string; label: string; tone?: string }) {
  return (
    <div class={"card" + (tone ? " card--" + tone : "")}>
      <div class="card__num">{n}</div>
      <div class="card__label">{label}</div>
    </div>
  );
}
