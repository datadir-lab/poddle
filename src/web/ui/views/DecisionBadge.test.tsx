import { describe, expect, it, afterEach } from "vitest";
import { render } from "preact";
import { DecisionBadge } from "./DecisionBadge";

function mount(vnode: any) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  render(vnode, container);
  return container;
}

let mounted: HTMLElement[] = [];
afterEach(() => {
  for (const el of mounted) render(null, el);
  mounted = [];
});

describe("DecisionBadge", () => {
  it.each([
    ["allow", "d-allow"],
    ["deny", "d-deny"],
    ["redact", "d-redact"],
    ["block", "d-block"],
  ])("decision %s maps to class %s", (decision, cls) => {
    const el = mount(<DecisionBadge decision={decision} />);
    mounted.push(el);
    const span = el.querySelector("span.decision");
    expect(span).not.toBeNull();
    expect(span!.classList.contains(cls)).toBe(true);
    expect(span!.textContent).toBe(decision);
  });

  it("renders a faint placeholder when decision is missing", () => {
    const el = mount(<DecisionBadge />);
    mounted.push(el);
    const span = el.querySelector("span.decision");
    expect(span).not.toBeNull();
    expect(span!.classList.contains("d-")).toBe(true);
    expect(span!.querySelector("span.faint")).not.toBeNull();
  });
});
