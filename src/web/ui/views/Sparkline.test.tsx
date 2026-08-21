import { describe, expect, it, afterEach } from "vitest";
import { render } from "preact";
import { Sparkline } from "./Sparkline";

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

describe("Sparkline", () => {
  it("renders an svg with a line for N (>=2) points", () => {
    const el = mount(<Sparkline data={[10, 20, 30, 40]} />);
    mounted.push(el);
    const svg = el.querySelector("svg.spark");
    expect(svg).not.toBeNull();
    expect(svg!.querySelector("polyline.spark__line")).not.toBeNull();
    expect(svg!.querySelector("circle.spark__dot")).not.toBeNull();
    expect(svg!.querySelector("polygon.spark__area")).not.toBeNull();
  });

  it("renders the empty state for 0 points", () => {
    const el = mount(<Sparkline data={[]} />);
    mounted.push(el);
    expect(el.querySelector("svg")).toBeNull();
    const empty = el.querySelector("span.spark--empty");
    expect(empty).not.toBeNull();
    expect(empty!.textContent).toBe("╌");
  });

  it("renders the empty state for a single point (still < 2)", () => {
    const el = mount(<Sparkline data={[42]} />);
    mounted.push(el);
    expect(el.querySelector("svg")).toBeNull();
    expect(el.querySelector("span.spark--empty")).not.toBeNull();
  });
});
