// Fact renders one label/value pair in a <dl> of pod facts (size, mode, policy,
// live cpu/mem). Lifted as-is from the dashboard's pod drill-down.
export function Fact({ label, children }: { label: string; children: any }) {
  return <div><dt>{label}</dt><dd>{children}</dd></div>;
}
