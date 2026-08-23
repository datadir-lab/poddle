import { describe, expect, test } from "vitest";
import { classify, categorize, suggestPolicy } from "./activity";
import type { Event } from "./types";

const ev = (o: Partial<Event>): Event => ({ seq: 0, time: "", kind: "request", ...o });

describe("classify", () => {
  test("maps known hosts to categories, unknown to other", () => {
    expect(classify("api.anthropic.com")).toBe("model");
    expect(classify("files.pythonhosted.org")).toBe("registry"); // .pythonhosted.org suffix
    expect(classify("github.com")).toBe("source");               // .github.com matches apex
    expect(classify("169.254.169.254")).toBe("metadata");
    expect(classify("generativelanguage.googleapis.com")).toBe("model"); // Gemini API, not all of googleapis
    expect(classify("storage.googleapis.com")).toBe("other");            // object storage is not a model API
    expect(classify("example.com")).toBe("other");
  });
});

describe("categorize", () => {
  test("rolls up by category with decision mix; methods empty when only tunnelled", () => {
    const events: Event[] = [
      ev({ upstream: "api.anthropic.com", method: "POST", decision: "allow", pod: "p" }),
      ev({ upstream: "pypi.org", method: "GET", decision: "redact", pod: "p" }),
      ev({ upstream: "github.com", method: "CONNECT", decision: "allow", pod: "p" }), // CONNECT is a tunnel, not a verb
      ev({ upstream: "evil.test", decision: "deny", pod: "p" }),
      ev({ upstream: "bad.test", decision: "block", pod: "p" }), // block decision
      ev({ kind: "pod.up", pod: "p" }), // non-request ignored
    ];
    const rolls = categorize(events);
    const model = rolls.find((r) => r.key === "model")!;
    expect(model.total).toBe(1);
    expect(model.methods).toEqual(["POST"]);
    const source = rolls.find((r) => r.key === "source")!;
    expect(source.methods).toEqual([]);          // tunnelled → no verb layer
    const other = rolls.find((r) => r.key === "other")!;
    expect(other.deny).toBe(1);
    expect(other.block).toBe(1);                 // block decision counted
    expect(rolls[rolls.length - 1].key).toBe("other"); // other sorts last
  });
});

describe("suggestPolicy", () => {
  test("allows reached hosts, scopes methods where seen, denies metadata, excludes blocked", () => {
    const events: Event[] = [
      ev({ upstream: "api.anthropic.com", method: "POST", decision: "allow", pod: "p" }),
      ev({ upstream: "github.com", method: "CONNECT", decision: "allow", pod: "p" }),        // tunnelled
      ev({ upstream: "pypi.org", method: "GET", decision: "redact", pod: "p" }),
      ev({ upstream: "evil.test", method: "POST", decision: "deny", pod: "p" }),  // excluded (deny)
      ev({ upstream: "bad.test", method: "GET", decision: "block", pod: "p" }),   // excluded (block)
    ];
    const p = suggestPolicy(events, "p-policy");
    expect(p.name).toBe("p-policy");
    expect(p.allow_upstreams!.sort()).toEqual(["api.anthropic.com", "github.com", "pypi.org"]);
    expect(p.allow_upstreams).not.toContain("evil.test");
    expect(p.allow_upstreams).not.toContain("bad.test"); // blocked host never auto-allowed
    expect(p.methods).toEqual({ "api.anthropic.com": ["POST"], "pypi.org": ["GET"] }); // github omitted (tunnelled)
    expect(p.deny_upstreams).toEqual(["169.254.169.254", "metadata.google.internal"]);
    expect(p.egress).toBe("redact");
  });
});
