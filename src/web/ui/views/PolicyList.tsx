import type { Policy } from "./types";

// PolicyList is the pure render half of the policies view's left-hand list.
// The container owns the fetch and the router: `hrefFor`/`newHref` build the
// visible hrefs (so a consumer's own org-scoped routes render correctly), and
// `linkTo` is the consumer's modifier-aware click-handler factory — a plain
// left-click does SPA nav, while ⌘/Ctrl/Shift/middle-click falls through to
// the browser's native "open in new tab" (no navigate()/preventDefault() logic
// lives in this file).
export function PolicyList({ policies, selectedName, hrefFor, newHref, linkTo }: {
  policies: Policy[];
  selectedName?: string;
  hrefFor: (name: string) => string;
  newHref: string;
  linkTo: (href: string) => (e: MouseEvent) => void;
}) {
  return (
    <div class="list">
      {policies.map((p) => (
        <a key={p.name} href={hrefFor(p.name)}
          onClick={linkTo(hrefFor(p.name))}
          class={selectedName === p.name ? "on" : ""}>{p.name}</a>
      ))}
      <a href={newHref} onClick={linkTo(newHref)} class="new">＋ New policy</a>
    </div>
  );
}
