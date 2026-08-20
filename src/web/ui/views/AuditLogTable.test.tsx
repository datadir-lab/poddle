import { describe, expect, it, afterEach } from "vitest";
import { render } from "preact";
import { act } from "preact/test-utils";
import { AuditLogTable } from "./AuditLogTable";
import type { Event } from "./types";

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

const EVENTS: Event[] = [
  { seq: 1, time: new Date().toISOString(), pod: "agent1", kind: "request", upstream: "api.anthropic.com", decision: "allow" },
  { seq: 2, time: new Date().toISOString(), pod: "agent2", kind: "block", upstream: "evil.example", decision: "block" },
  { seq: 3, time: new Date().toISOString(), pod: "agent1", kind: "request", upstream: "metadata.google.internal", decision: "deny" },
];

describe("AuditLogTable", () => {
  it("renders a row for each event given", () => {
    const el = mount(<AuditLogTable events={EVENTS} />);
    mounted.push(el);
    const rows = el.querySelectorAll("tbody tr");
    expect(rows.length).toBe(3);
    expect(el.textContent).toContain("api.anthropic.com");
    expect(el.textContent).toContain("evil.example");
    expect(el.textContent).toContain("metadata.google.internal");
    expect(el.textContent).toContain("3 events");
  });

  it("renders the empty state when there are no events", () => {
    const el = mount(<AuditLogTable events={[]} />);
    mounted.push(el);
    expect(el.textContent).toContain("Monitoring active — no events recorded yet.");
  });

  it("narrows rows when typing in the filter input", () => {
    const el = mount(<AuditLogTable events={EVENTS} />);
    mounted.push(el);
    const input = el.querySelector("input.grow") as HTMLInputElement;
    expect(input).not.toBeNull();

    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
    act(() => {
      setter.call(input, "evil");
      input.dispatchEvent(new Event_("input", { bubbles: true }));
    });

    const rows = el.querySelectorAll("tbody tr");
    expect(rows.length).toBe(1);
    expect(el.textContent).toContain("evil.example");
    expect(el.textContent).not.toContain("api.anthropic.com");
    expect(el.textContent).toContain("1 events");
  });

  it("seeds the filter from initialPod", () => {
    const el = mount(<AuditLogTable events={EVENTS} initialPod="agent2" />);
    mounted.push(el);
    const rows = el.querySelectorAll("tbody tr");
    expect(rows.length).toBe(1);
    expect(el.textContent).toContain("evil.example");
  });
});

// InputEvent isn't always constructible identically across jsdom versions;
// use the DOM Event constructor directly (bubbles is all preact needs to see it).
const Event_ = globalThis.Event;
