import { describe, expect, it, afterEach, vi } from "vitest";
import { render } from "preact";
import { act } from "preact/test-utils";
import { PolicyEditor } from "./PolicyEditor";
import type { Policy } from "./types";

function mount(vnode: any) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  // Wrapped in act() so PolicyEditor's mount-time effect (which syncs form
  // state from the `policy` prop) is flushed synchronously — otherwise it
  // fires later via the real rAF/jsdom scheduler and clobbers edits made
  // right after mount.
  act(() => { render(vnode, container); });
  return container;
}

let mounted: HTMLElement[] = [];
afterEach(() => {
  for (const el of mounted) render(null, el);
  mounted = [];
});

const POLICY: Policy = {
  name: "prod",
  allow_upstreams: ["api.anthropic.com"],
  deny_upstreams: ["evil.example"],
  methods: { "git.internal": ["GET"] },
  egress: "block",
};

function setValue(el: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, "value")!.set!;
  act(() => {
    setter.call(el, value);
    el.dispatchEvent(new Event_("input", { bubbles: true }));
  });
}

describe("PolicyEditor", () => {
  it("renders the policy's fields", () => {
    const el = mount(
      <PolicyEditor policy={POLICY} onSave={async () => ({ ok: true })} onDelete={async () => {}} />,
    );
    mounted.push(el);
    expect((el.querySelector("#pol-name") as HTMLInputElement).value).toBe("prod");
    expect((el.querySelector("#pol-allow") as HTMLTextAreaElement).value).toBe("api.anthropic.com");
    expect((el.querySelector("#pol-deny") as HTMLTextAreaElement).value).toBe("evil.example");
    expect((el.querySelector("#pol-methods") as HTMLTextAreaElement).value).toContain("git.internal");
    expect(el.querySelector('[role="radio"][aria-checked="true"]')?.textContent).toBe("Block");
  });

  it("calls onSave with the edited policy when Save is clicked, with no injected /v1 code", async () => {
    const onSave = vi.fn().mockResolvedValue({ ok: true });
    const el = mount(
      <PolicyEditor policy={POLICY} onSave={onSave} onDelete={async () => {}} />,
    );
    mounted.push(el);

    setValue(el.querySelector("#pol-name") as HTMLInputElement, "prod-2");

    await act(async () => {
      (el.querySelector(".btn--primary") as HTMLButtonElement).click();
    });

    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith({
      name: "prod-2",
      allow_upstreams: ["api.anthropic.com"],
      deny_upstreams: ["evil.example"],
      methods: { "git.internal": ["GET"] },
      egress: "block",
    });
    expect(el.querySelector(".err")).toBeNull();
  });

  it('shows the error when onSave resolves {ok:false,error:"boom"}', async () => {
    const onSave = vi.fn().mockResolvedValue({ ok: false, error: "boom" });
    const el = mount(
      <PolicyEditor policy={POLICY} onSave={onSave} onDelete={async () => {}} />,
    );
    mounted.push(el);

    await act(async () => {
      (el.querySelector(".btn--primary") as HTMLButtonElement).click();
    });

    expect(el.querySelector(".err")?.textContent).toBe("boom");
  });

  it("calls onDelete when Delete is clicked", async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);
    const el = mount(
      <PolicyEditor policy={POLICY} onSave={async () => ({ ok: true })} onDelete={onDelete} />,
    );
    mounted.push(el);

    await act(async () => {
      (el.querySelector(".btn--danger") as HTMLButtonElement).click();
    });

    expect(onDelete).toHaveBeenCalledTimes(1);
  });

  it("renders the injected hint slot instead of a hardcoded literal", () => {
    const el = mount(
      <PolicyEditor
        policy={POLICY}
        onSave={async () => ({ ok: true })}
        onDelete={async () => {}}
        hint={<span class="my-hint">custom hint copy</span>}
      />,
    );
    mounted.push(el);
    expect(el.querySelector(".hint .my-hint")?.textContent).toBe("custom hint copy");
  });
});

// InputEvent isn't always constructible identically across jsdom versions;
// use the DOM Event constructor directly (bubbles is all preact needs to see it).
const Event_ = globalThis.Event;
