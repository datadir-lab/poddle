// PoddleMark is the real product mark (the isometric pod cube from the site
// favicon). The two side faces ride on currentColor (= --ink) so the logo reads
// on both the cream and the near-black rails; the top face and the prompt glyph
// keep the fixed brand green.
export function PoddleMark({ size = 30 }: { size?: number }) {
  return (
    <svg class="pmark" width={size} height={size} viewBox="382.0 134.1 435.9 435.9" aria-hidden="true">
      <path d="M769.71,450.00 L769.71,254.04 L600.00,352.02 L600.00,547.98 Z" fill="currentColor" />
      <path d="M600.00,547.98 L600.00,352.02 L430.29,254.04 L430.29,450.00 Z" fill="currentColor" />
      <path d="M769.71,254.04 L600.00,156.06 L430.29,254.04 L600.00,352.02 Z" fill="#2f9e6f" />
      <g transform="matrix(169.7056,97.9787,0.0000,195.9601,430.29,254.04)" fill="#2f9e6f">
        <path d="M0.19,0.31 L0.29,0.31 L0.44,0.50 L0.29,0.69 L0.19,0.69 L0.34,0.50 Z" />
        <path d="M0.50,0.605 L0.72,0.605 L0.72,0.685 L0.50,0.685 Z" />
      </g>
    </svg>
  );
}
