import { describe, expect, it, afterEach, vi } from "vitest";
import { render } from "preact";
import { act } from "preact/test-utils";
import { PolicyEditor } from "./PolicyEditor";
import type { Policy, PolicyTemplate } from "./types";

// PolicyEditor is presentational: the mutations are injected. These tests drive
// the visual builder with fakes (no network) and assert the callback contract —
// Save hands the container the *built* policy, a failed save surfaces its error
// without wiping the form, and Delete delegates to onDelete.
//
// act() flushes Preact's effects + queued renders synchronously, so the mount
// effect that seeds state from `policy` settles before we interact (in a real
// browser it runs at mount, long before the user types; jsdom would otherwise
// flush it mid-test).

function mount(vnode: any) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  act(() => { render(vnode, container); });
  return container;
}

function setValue(el: HTMLInputElement, v: string) {
  act(() => {
    el.value = v;
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

function findButton(root: HTMLElement, label: string) {
  return [...root.querySelectorAll("button")].find((b) => b.textContent === label) as HTMLButtonElement;
}

let mounted: HTMLElement[] = [];
afterEach(() => {
  for (const el of mounted) render(null, el);
  mounted = [];
});

const POLICY: Policy = {
  name: "prod",
  allow_upstreams: ["api.example.com"],
  deny_upstreams: [],
  methods: { "api.example.com": ["GET"] },
  egress: "redact",
};

const noopSave = () => Promise.resolve({ ok: true });
const noopDelete = () => Promise.resolve();

describe("PolicyEditor", () => {
  it("renders a builder row for every toRows(policy) entry", () => {
    const el = mount(<PolicyEditor policy={POLICY} events={[]} scopePods={[]} onSave={noopSave} onDelete={noopDelete} />);
    mounted.push(el);
    const hosts = el.querySelectorAll("input.rule__host");
    expect(hosts.length).toBe(1);
    expect((hosts[0] as HTMLInputElement).value).toBe("api.example.com");
    // the GET method restriction collapses to a clickable summary
    expect(el.querySelector(".rule__msum")?.textContent).toContain("GET");
    expect((el.querySelector("#pol-name") as HTMLInputElement).value).toBe("prod");
  });

  it("Save calls onSave with the built policy (edited name + builder rows)", async () => {
    const onSave = vi.fn().mockResolvedValue({ ok: true });
    const el = mount(<PolicyEditor policy={POLICY} events={[]} scopePods={[]} onSave={onSave} onDelete={noopDelete} />);
    mounted.push(el);

    setValue(el.querySelector("#pol-name") as HTMLInputElement, "renamed");
    await act(async () => { findButton(el, "Save").click(); });

    expect(onSave).toHaveBeenCalledTimes(1);
    const built = onSave.mock.calls[0][0] as Policy;
    expect(built.name).toBe("renamed");
    expect(built.allow_upstreams).toEqual(["api.example.com"]);
    expect(built.methods).toEqual({ "api.example.com": ["GET"] });
    expect(built.egress).toBe("redact");
  });

  it("a failed save shows the error and does not clear the form", async () => {
    const onSave = vi.fn().mockResolvedValue({ ok: false, error: "x" });
    const el = mount(<PolicyEditor policy={POLICY} events={[]} scopePods={[]} onSave={onSave} onDelete={noopDelete} />);
    mounted.push(el);

    await act(async () => { findButton(el, "Save").click(); });

    expect(el.querySelector(".err")?.textContent).toContain("x");
    // the form is intact — the name and the builder row survive a failed save
    expect((el.querySelector("#pol-name") as HTMLInputElement).value).toBe("prod");
    expect(el.querySelectorAll("input.rule__host").length).toBe(1);
  });

  it("Delete calls onDelete", async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);
    const el = mount(<PolicyEditor policy={POLICY} events={[]} scopePods={[]} onSave={noopSave} onDelete={onDelete} />);
    mounted.push(el);

    await act(async () => { findButton(el, "Delete").click(); });

    expect(onDelete).toHaveBeenCalledTimes(1);
  });

  const TEMPLATES: PolicyTemplate[] = [
    { id: "provider-only", label: "AI provider only", hint: "Model only.", policy: { allow_upstreams: ["api.anthropic.com"], deny_upstreams: ["169.254.169.254"], methods: {}, egress: "redact" } },
    { id: "locked-down", label: "Locked down", hint: "Fail closed.", policy: { allow_upstreams: ["api.anthropic.com"], deny_upstreams: ["169.254.169.254"], methods: {}, egress: "block" } },
  ];
  const NEW: Policy = { name: "", egress: "redact" };

  it("a blank new policy offers the templates, and applying one fills the builder and hides the picker", () => {
    const el = mount(<PolicyEditor policy={NEW} events={[]} scopePods={[]} onSave={noopSave} onDelete={noopDelete} templates={TEMPLATES} />);
    mounted.push(el);

    // The picker offers one .tmpl per template on a blank draft.
    expect(el.querySelectorAll(".tmpl").length).toBe(2);
    act(() => { findButton(el, "Locked downFail closed.").click(); });

    // Name + allow-list + deny-list populate; egress flips to the template's mode.
    expect((el.querySelector("#pol-name") as HTMLInputElement).value).toBe("locked-down");
    expect((el.querySelector("input.rule__host") as HTMLInputElement).value).toBe("api.anthropic.com");
    expect((el.querySelector("input[aria-label='Blocked host']") as HTMLInputElement).value).toBe("169.254.169.254");
    // The picker collapses once the builder is no longer blank.
    expect(el.querySelectorAll(".tmpl").length).toBe(0);
  });

  it("the templates picker never shows for an existing (named) policy", () => {
    const el = mount(<PolicyEditor policy={POLICY} events={[]} scopePods={[]} onSave={noopSave} onDelete={noopDelete} templates={TEMPLATES} />);
    mounted.push(el);
    expect(el.querySelectorAll(".tmpl").length).toBe(0);
  });

  it("Set as default toggles via onSetDefault and reflects isDefault", () => {
    const onSetDefault = vi.fn();
    const el = mount(<PolicyEditor policy={POLICY} events={[]} scopePods={[]} onSave={noopSave} onDelete={noopDelete} onSetDefault={onSetDefault} />);
    mounted.push(el);
    act(() => { findButton(el, "Set as default").click(); });
    expect(onSetDefault).toHaveBeenCalledWith("prod");

    // When already the default, the button reads "★ Default" and clears on click.
    const el2 = mount(<PolicyEditor policy={POLICY} events={[]} scopePods={[]} onSave={noopSave} onDelete={noopDelete} isDefault onSetDefault={onSetDefault} />);
    mounted.push(el2);
    const btn = findButton(el2, "★ Default");
    expect(btn).toBeTruthy();
    act(() => { btn.click(); });
    expect(onSetDefault).toHaveBeenLastCalledWith("");
  });

  it("flags a wide-open policy, and the metadata fix adds the guard", () => {
    // A named policy with no allow-list is wide open -> advisories fire.
    const el = mount(<PolicyEditor policy={{ name: "open", egress: "redact" }} events={[]} scopePods={[]} onSave={noopSave} onDelete={noopDelete} />);
    mounted.push(el);
    const texts = [...el.querySelectorAll(".advisory")].map((a) => a.textContent || "");
    expect(texts.some((t) => t.includes("every host is reachable"))).toBe(true);
    expect(texts.some((t) => t.includes("metadata endpoints are reachable"))).toBe(true);

    // The one-click fix adds the two metadata hosts to the deny-list.
    act(() => { findButton(el, "Block them").click(); });
    const blocked = [...el.querySelectorAll("input[aria-label='Blocked host']")].map((i) => (i as HTMLInputElement).value);
    expect(blocked).toContain("169.254.169.254");
    expect(blocked).toContain("metadata.google.internal");
  });

  it("a monitored policy with no would-be denials nudges to enforce, and Enforce now saves monitor off", async () => {
    const onSave = vi.fn().mockResolvedValue({ ok: true });
    const events = [{ seq: 1, time: new Date().toISOString(), pod: "a", kind: "request", upstream: "api.example.com", decision: "allow" }];
    const el = mount(<PolicyEditor policy={{ ...POLICY, monitor: true }} events={events as any} scopePods={["a"]} isSaved onSave={onSave} onDelete={noopDelete} />);
    mounted.push(el);

    // Traffic, zero monitor hits -> the safe-to-enforce state.
    expect(el.querySelector(".rollout--clear")).toBeTruthy();
    await act(async () => { findButton(el, "Enforce now").click(); });
    expect((onSave.mock.calls[0][0] as Policy).monitor).toBeFalsy();
  });

  it("a monitored policy with logged would-be denials warns and offers no enforce button", () => {
    const events = [
      { seq: 2, time: new Date().toISOString(), pod: "a", kind: "request", upstream: "x.example", decision: "monitor" },
      { seq: 1, time: new Date().toISOString(), pod: "a", kind: "request", upstream: "api.example.com", decision: "allow" },
    ];
    const el = mount(<PolicyEditor policy={{ ...POLICY, monitor: true }} events={events as any} scopePods={["a"]} isSaved onSave={noopSave} onDelete={noopDelete} />);
    mounted.push(el);
    expect(el.querySelector(".rollout--warn")).toBeTruthy();
    expect(findButton(el, "Enforce now")).toBeFalsy();
  });

  it("the request probe evaluates against the current draft", () => {
    const el = mount(<PolicyEditor policy={POLICY} events={[]} scopePods={[]} onSave={noopSave} onDelete={noopDelete} />);
    mounted.push(el);
    // POLICY allows api.example.com (GET only): a non-allowed host is denied,
    // the allowed host (default probe method GET) passes.
    setValue(el.querySelector(".probe__host") as HTMLInputElement, "evil.example");
    expect(el.querySelector(".probe__result")?.textContent).toContain("deny");
    setValue(el.querySelector(".probe__host") as HTMLInputElement, "api.example.com");
    expect(el.querySelector(".probe__result")?.textContent).toContain("allow");
  });

  it("Duplicate hands onDuplicate the built policy", () => {
    const onDuplicate = vi.fn();
    const el = mount(<PolicyEditor policy={POLICY} events={[]} scopePods={[]} onSave={noopSave} onDelete={noopDelete} onDuplicate={onDuplicate} />);
    mounted.push(el);
    act(() => { findButton(el, "Duplicate").click(); });
    expect(onDuplicate).toHaveBeenCalledTimes(1);
    expect((onDuplicate.mock.calls[0][0] as Policy).allow_upstreams).toEqual(["api.example.com"]);
  });

  it("the enforcement toggle writes the monitor flag", async () => {
    const onSave = vi.fn().mockResolvedValue({ ok: true });
    const el = mount(<PolicyEditor policy={POLICY} events={[]} scopePods={[]} onSave={onSave} onDelete={noopDelete} />);
    mounted.push(el);

    // Default is Enforce -> no monitor flag on the built policy.
    await act(async () => { findButton(el, "Save").click(); });
    expect((onSave.mock.calls[0][0] as Policy).monitor).toBeFalsy();

    // Flip to Monitor -> the built policy carries monitor: true.
    act(() => { findButton(el, "Monitor").click(); });
    await act(async () => { findButton(el, "Save").click(); });
    expect((onSave.mock.calls[1][0] as Policy).monitor).toBe(true);
  });

  it("treats a named policy as unsaved when isSaved is false (a duplicate seed)", () => {
    const el = mount(<PolicyEditor policy={{ ...POLICY, name: "prod-copy" }} events={[]} scopePods={[]} onSave={noopSave} onDelete={noopDelete} isSaved={false} onDuplicate={vi.fn()} onSetDefault={vi.fn()} />);
    mounted.push(el);
    // Delete / Duplicate / Set-as-default are hidden until the copy is saved.
    expect(findButton(el, "Delete")).toBeFalsy();
    expect(findButton(el, "Duplicate")).toBeFalsy();
    expect((el.querySelector("#pol-name") as HTMLInputElement).value).toBe("prod-copy");
  });
});
