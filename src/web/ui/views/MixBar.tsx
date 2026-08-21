import type { Dest } from "./types";

// MixBar draws a destination's decision split as a compact proportional bar.
export function MixBar({ d }: { d: Dest }) {
  const segs = ([["allow", d.allow], ["redact", d.redact], ["deny", d.deny], ["block", d.block]] as const).filter(([, n]) => n > 0);
  return (
    <span class="mix" role="img" aria-label={segs.map(([k, n]) => `${n} ${k}`).join(", ")}>
      {segs.map(([k, n]) => <span key={k} class={"mix__seg d-" + k} style={`flex-grow:${n}`} title={`${k}: ${n}`} />)}
    </span>
  );
}
