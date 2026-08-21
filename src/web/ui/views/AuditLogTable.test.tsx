import { describe, expect, it, afterEach, vi } from "vitest";
import { render } from "preact";
import { AuditLogTable } from "./AuditLogTable";
import type { Event } from "./types";

// jsdom doesn't implement the Blob URL APIs downloadCsv uses — stub them so
// clicking Export doesn't throw (the CSV bytes themselves are covered by e2e).
if (typeof URL.createObjectURL !== "function") {
  (URL as any).createObjectURL = () => "blob:mock";
  (URL as any).revokeObjectURL = () => {};
}

function mount(vnode: any) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  render(vnode, container);
  return container;
}

// Preact batches state updates via a microtask; flush it before asserting DOM.
async function tick() {
  await Promise.resolve();
  await Promise.resolve();
}

function setValue(el: HTMLInputElement, v: string) {
  el.value = v;
  el.dispatchEvent(new Event("input", { bubbles: true }));
}

let mounted: HTMLElement[] = [];
afterEach(() => {
  for (const el of mounted) render(null, el);
  mounted = [];
});

const EVENTS: Event[] = [
  { seq: 3, time: new Date().toISOString(), pod: "agent1", kind: "request", upstream: "api.anthropic.com", decision: "allow" },
  { seq: 2, time: new Date().toISOString(), pod: "agent1", kind: "request", upstream: "api.github.com", decision: "redact", detail: "redacted 1 secret(s)" },
  { seq: 1, time: new Date().toISOString(), pod: "agent2", kind: "request", upstream: "metadata.google.internal", decision: "deny" },
];

describe("AuditLogTable", () => {
  it("renders a row for every event", () => {
    const el = mount(<AuditLogTable events={EVENTS} loading={false} />);
    mounted.push(el);
    expect(el.querySelectorAll("tr.auditrow").length).toBe(3);
    expect(el.textContent).toContain("api.anthropic.com");
    expect(el.textContent).toContain("metadata.google.internal");
  });

  it("the text filter narrows to matching rows", async () => {
    const el = mount(<AuditLogTable events={EVENTS} loading={false} />);
    mounted.push(el);
    const input = el.querySelector("input.grow") as HTMLInputElement;
    setValue(input, "github");
    await tick();
    expect(el.querySelectorAll("tr.auditrow").length).toBe(1);
    expect(el.textContent).toContain("api.github.com");
    expect(el.textContent).not.toContain("api.anthropic.com");
  });

  it("the decision filter narrows to matching rows", async () => {
    const el = mount(<AuditLogTable events={EVENTS} loading={false} />);
    mounted.push(el);
    const deny = [...el.querySelectorAll('[role="radio"]')].find((r) => r.textContent?.startsWith("Deny")) as HTMLElement;
    deny.click();
    await tick();
    expect(el.querySelectorAll("tr.auditrow").length).toBe(1);
    expect(el.textContent).toContain("metadata.google.internal");
  });

  it("invokes onExport with the currently-filtered rows on Export click", async () => {
    const onExport = vi.fn();
    const el = mount(<AuditLogTable events={EVENTS} loading={false} onExport={onExport} />);
    mounted.push(el);
    const input = el.querySelector("input.grow") as HTMLInputElement;
    setValue(input, "github");
    await tick();
    const btn = [...el.querySelectorAll("button")].find((b) => b.textContent === "Export CSV") as HTMLButtonElement;
    btn.click();
    expect(onExport).toHaveBeenCalledTimes(1);
    expect(onExport.mock.calls[0][0]).toHaveLength(1);
    expect(onExport.mock.calls[0][0][0].upstream).toBe("api.github.com");
  });
});
