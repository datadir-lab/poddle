import { describe, expect, it, afterEach, vi } from "vitest";
import { render } from "preact";
import { SegmentedControl } from "./SegmentedControl";

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

const OPTIONS = [
  { value: "", label: "All" },
  { value: "allow", label: "Allow" },
  { value: "deny", label: "Deny" },
];

describe("SegmentedControl", () => {
  it("renders every option as a radio button, checking the active one", () => {
    const el = mount(<SegmentedControl value="allow" options={OPTIONS} onChange={() => {}} ariaLabel="test" />);
    mounted.push(el);
    const group = el.querySelector('[role="radiogroup"]');
    expect(group).not.toBeNull();
    expect(group!.getAttribute("aria-label")).toBe("test");
    const radios = el.querySelectorAll('[role="radio"]');
    expect(radios.length).toBe(OPTIONS.length);
    expect(radios[0].textContent).toBe("All");
    expect(radios[1].getAttribute("aria-checked")).toBe("true");
    expect(radios[1].classList.contains("on")).toBe(true);
    expect(radios[0].getAttribute("aria-checked")).toBe("false");
  });

  it("renders a badge when an option carries one", () => {
    const withBadge = [{ value: "", label: "All", badge: 7 }, ...OPTIONS.slice(1)];
    const el = mount(<SegmentedControl value="" options={withBadge} onChange={() => {}} ariaLabel="test" />);
    mounted.push(el);
    const badge = el.querySelector(".seg__badge");
    expect(badge).not.toBeNull();
    expect(badge!.textContent).toBe("7");
  });

  it("ArrowRight moves the selection to the next option and calls onChange", () => {
    const onChange = vi.fn();
    const el = mount(<SegmentedControl value="" options={OPTIONS} onChange={onChange} ariaLabel="test" />);
    mounted.push(el);
    const group = el.querySelector('[role="radiogroup"]') as HTMLElement;
    group.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true }));
    expect(onChange).toHaveBeenCalledWith("allow");
  });

  it("ArrowLeft wraps around to the last option and calls onChange", () => {
    const onChange = vi.fn();
    const el = mount(<SegmentedControl value="" options={OPTIONS} onChange={onChange} ariaLabel="test" />);
    mounted.push(el);
    const group = el.querySelector('[role="radiogroup"]') as HTMLElement;
    group.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true, cancelable: true }));
    expect(onChange).toHaveBeenCalledWith("deny");
  });

  it("clicking an option calls onChange with its value", () => {
    const onChange = vi.fn();
    const el = mount(<SegmentedControl value="" options={OPTIONS} onChange={onChange} ariaLabel="test" />);
    mounted.push(el);
    const radios = el.querySelectorAll('[role="radio"]');
    (radios[2] as HTMLElement).click();
    expect(onChange).toHaveBeenCalledWith("deny");
  });
});
