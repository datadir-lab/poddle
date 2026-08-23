import { afterEach, describe, expect, it } from "vitest";
import { render } from "preact";
import { act } from "preact/test-utils";
import { ActivityProfile } from "./ActivityProfile";
import type { Event, Policy } from "./types";

// ActivityProfile is presentational: it derives its rollups from `events` via
// the (already-tested) categorize/suggestPolicy, and hands a suggested policy
// up through onSuggestPolicy on click — mirrors PolicyEditor.test.tsx's
// mount/act/findButton conventions.

function mount(vnode: any) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  act(() => { render(vnode, container); });
  return container;
}

function findButton(root: HTMLElement, label: RegExp) {
  return [...root.querySelectorAll("button")].find((b) => label.test(b.textContent || "")) as HTMLButtonElement;
}

let mounted: HTMLElement[] = [];
afterEach(() => {
  for (const el of mounted) render(null, el);
  mounted = [];
});

const ev = (o: Partial<Event>): Event => ({ seq: 0, time: "", kind: "request", ...o });

describe("ActivityProfile", () => {
  it("shows categories and suggests a policy from the pod's reached hosts", () => {
    const events: Event[] = [
      ev({ upstream: "api.anthropic.com", method: "POST", decision: "allow", pod: "p" }),
      ev({ upstream: "evil.test", decision: "deny", pod: "p" }),
    ];
    let suggested: Policy | null = null;
    const el = mount(
      <ActivityProfile
        podName="p"
        events={events}
        onSuggestPolicy={(pp) => { suggested = pp; }}
      />,
    );
    mounted.push(el);

    expect(el.textContent).toContain("Model API");

    const btn = findButton(el, /create policy/i);
    act(() => { btn.click(); });

    expect(suggested!.allow_upstreams).toEqual(["api.anthropic.com"]);
    expect(suggested!.allow_upstreams).not.toContain("evil.test");
  });

  it("shows an empty state when the pod has no recorded egress", () => {
    const el = mount(<ActivityProfile podName="p" events={[]} onSuggestPolicy={() => {}} />);
    mounted.push(el);
    expect(el.textContent).toContain("No egress recorded");
    expect(findButton(el, /create policy/i)).toBeFalsy();
  });

  it("disables the policy button for an all-denied pod (no allow-all suggestion)", () => {
    const events: Event[] = [
      ev({ upstream: "evil.test", decision: "deny", pod: "p" }),
    ];
    let suggested: Policy | null = null;
    const el = mount(
      <ActivityProfile
        podName="p"
        events={events}
        onSuggestPolicy={(pp) => { suggested = pp; }}
      />,
    );
    mounted.push(el);

    // Profile still renders (non-empty) even though nothing was allowed.
    expect(el.textContent).toContain("Other");

    const btn = findButton(el, /create policy/i);
    expect(btn.disabled).toBe(true);

    act(() => { btn.click(); });
    expect(suggested).toBeNull();
  });
});
