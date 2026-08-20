import type { SegOption } from "./types";

// SegmentedControl is an accessible single-select control (role=radiogroup) for
// a small set of mutually exclusive options that should all stay visible with
// immediate effect — the right pattern for egress mode and the audit filter,
// and it keeps the bundle dependency-free for go:embed. An option's `tone`
// colors the active segment by its meaning (e.g. block = deny-red).
export function SegmentedControl({ value, options, onChange, ariaLabel }: {
  value: string; options: SegOption[]; onChange: (v: string) => void; ariaLabel: string;
}) {
  const idx = Math.max(0, options.findIndex((o) => o.value === value));
  const onKey = (e: KeyboardEvent) => {
    let ni = idx;
    if (e.key === "ArrowRight" || e.key === "ArrowDown") ni = (idx + 1) % options.length;
    else if (e.key === "ArrowLeft" || e.key === "ArrowUp") ni = (idx - 1 + options.length) % options.length;
    else return;
    e.preventDefault();
    onChange(options[ni].value);
  };
  return (
    <div class="seg" role="radiogroup" aria-label={ariaLabel} onKeyDown={onKey}>
      {options.map((o, i) => (
        <button type="button" role="radio" aria-checked={value === o.value} data-tone={o.tone}
          tabIndex={i === idx ? 0 : -1}
          class={"seg__opt" + (value === o.value ? " on" : "")}
          onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}
