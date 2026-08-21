import { DECISIONS } from "./aggregate";
import { Icon } from "./Icon";

// PostureBar: the decision mix as a single proportional bar + a labelled legend.
// Segments carry a status colour, a glyph, and a count — identity never rests on
// colour alone (deny and block share the red, told apart by their icons/labels).
export function PostureBar({ counts }: { counts: Record<string, number> }) {
  const total = DECISIONS.reduce((s, d) => s + (counts[d.key] || 0), 0);
  if (total === 0) return <div class="chart-empty">No decisions recorded yet.</div>;
  const pct = (v: number) => Math.round((v / total) * 100);
  return (
    <div class="posture">
      <div class="posture__bar" role="img"
        aria-label={"Decision mix: " + DECISIONS.map((d) => `${counts[d.key] || 0} ${d.label}`).join(", ")}>
        {DECISIONS.filter((d) => (counts[d.key] || 0) > 0).map((d) => (
          <div key={d.key} class={"posture__seg d-" + d.key} style={`flex-grow:${counts[d.key]}`}
            title={`${d.label}: ${counts[d.key]} (${pct(counts[d.key])}%)`} />
        ))}
      </div>
      <ul class="legend">
        {DECISIONS.map((d) => (
          <li key={d.key} class="legend__i">
            <span class={"legend__mk d-" + d.key}><Icon name={d.icon} size={13} /></span>
            <span class="legend__lb">{d.label}</span>
            <span class="legend__v">{counts[d.key] || 0}</span>
            <span class="legend__pc">{pct(counts[d.key] || 0)}%</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
