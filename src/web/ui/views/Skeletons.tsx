// Skeletons fill the brief gap before the first fetch resolves, so a populated
// account never flashes its empty state on load.
export function SkelCards() {
  return (
    <div class="cards" aria-hidden="true">
      {[0, 1, 2, 3].map((i) => (
        <div class="card" key={i}><span class="skel skel--num" /><span class="skel skel--sm" /></div>
      ))}
    </div>
  );
}
export function SkelTable({ rows = 6 }: { rows?: number }) {
  return (
    <div class="table-wrap skel-table" aria-hidden="true" aria-busy="true">
      {Array.from({ length: rows }).map((_, i) => <div class="skel-tr" key={i}><span class="skel" /></div>)}
    </div>
  );
}
