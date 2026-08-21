import type { Policy } from "./types";

// PolicyList is the pure render half of the policies view's left-hand list.
// The container owns the fetch and the router: `hrefFor`/`newHref` build the
// visible hrefs (so a consumer's own org-scoped routes render correctly), and
// `linkTo` is the consumer's modifier-aware click-handler factory — a plain
// left-click does SPA nav, while ⌘/Ctrl/Shift/middle-click falls through to
// the browser's native "open in new tab" (no navigate()/preventDefault() logic
// lives in this file). `usage` maps a policy name to how many running pods it
// governs (the fleet-governance badge); `loading` renders the skeleton rows
// while the policy list is still in flight.
export function PolicyList({ policies, selectedName, loading, usage, hrefFor, newHref, linkTo }: {
  policies: Policy[];
  selectedName?: string;
  loading: boolean;
  usage: (name: string) => number;
  hrefFor: (name: string) => string;
  newHref: string;
  linkTo: (href: string) => (e: MouseEvent) => void;
}) {
  return (
    <div class="list">
      {loading
        ? [0, 1, 2].map((i) => <span class="list__skel skel" key={i} aria-hidden="true" />)
        : policies.map((p) => {
            const n = usage(p.name);
            return (
              <a key={p.name} href={hrefFor(p.name)} onClick={linkTo(hrefFor(p.name))}
                class={"list__row" + (selectedName === p.name ? " on" : "")}>
                <span>{p.name}</span>
                {n > 0 && <span class="list__meta" title={`${n} running pod${n === 1 ? "" : "s"} use this policy`}>{n} pod{n === 1 ? "" : "s"}</span>}
              </a>
            );
          })}
      <a href={newHref} onClick={linkTo(newHref)} class="new">＋ New policy</a>
    </div>
  );
}
