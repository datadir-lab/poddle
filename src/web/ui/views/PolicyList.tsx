import type { Policy } from "./types";

// PolicyList is the pure render half of the policies view's left-hand list.
// The container owns the fetch and the router: `onSelect`/`onNew` are plain
// semantic callbacks (no navigate()/linkTo() code lives in this file).
export function PolicyList({ policies, selectedName, onSelect, onNew }: {
  policies: Policy[];
  selectedName?: string;
  onSelect: (name: string) => void;
  onNew: () => void;
}) {
  return (
    <div class="list">
      {policies.map((p) => (
        <a key={p.name} href={`/policies/${encodeURIComponent(p.name)}`}
          onClick={(e: MouseEvent) => { e.preventDefault(); onSelect(p.name); }}
          class={selectedName === p.name ? "on" : ""}>{p.name}</a>
      ))}
      <a href="/policies/new" onClick={(e: MouseEvent) => { e.preventDefault(); onNew(); }} class="new">＋ New policy</a>
    </div>
  );
}
