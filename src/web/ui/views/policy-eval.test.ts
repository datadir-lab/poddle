import { describe, expect, test } from "vitest";
import { decide } from "./policy-eval";
import type { Policy } from "./types";

// These rules are what the dashboard e2e suite (dashboard.spec.ts) relies on
// for its dry-run assertions — see roughly lines 210, 284, and 591 there.

describe("decide", () => {
  test("an allow-listed host is allowed", () => {
    const pol: Policy = { name: "p", allow_upstreams: ["api.github.com"] };
    expect(decide(pol, "api.github.com", "GET").decision).toBe("allow");
  });

  test("the deny-list wins even when the host is also allow-listed", () => {
    const pol: Policy = {
      name: "p",
      allow_upstreams: ["metadata.google.internal"],
      deny_upstreams: ["metadata.google.internal"],
    };
    expect(decide(pol, "metadata.google.internal", "GET").decision).toBe("deny");
  });

  test("a per-method restriction allows a listed method", () => {
    const pol: Policy = {
      name: "p",
      allow_upstreams: ["api.github.com"],
      methods: { "api.github.com": ["GET"] },
    };
    expect(decide(pol, "api.github.com", "GET").decision).toBe("allow");
  });

  test("a per-method restriction blocks an unlisted method", () => {
    const pol: Policy = {
      name: "p",
      allow_upstreams: ["api.github.com"],
      methods: { "api.github.com": ["GET"] },
    };
    expect(decide(pol, "api.github.com", "POST").decision).toBe("block");
  });

  test("an unlisted host is denied by default when the allow-list is non-empty", () => {
    const pol: Policy = { name: "p", allow_upstreams: ["api.github.com"] };
    expect(decide(pol, "unlisted.example", "GET").decision).toBe("deny");
  });

  test("a '.suffix' allow rule matches a subdomain", () => {
    const pol: Policy = { name: "p", allow_upstreams: [".github.com"] };
    expect(decide(pol, "api.github.com", "GET").decision).toBe("allow");
  });

  test("a '.suffix' allow rule does not match an unrelated host", () => {
    const pol: Policy = { name: "p", allow_upstreams: [".github.com"] };
    expect(decide(pol, "evil.example", "GET").decision).toBe("deny");
  });
});
