import { describe, expect, test } from "vitest";
import { decide, dryRun } from "./policy-eval";
import type { Event, Policy } from "./types";

// These rules are what the dashboard e2e suite (dashboard.spec.ts) relies on
// for its dry-run assertions — see roughly lines 210, 284, and 591 there.

describe("decide", () => {
  test("an allow-listed host is allowed", () => {
    const pol: Policy = { name: "p", allow_upstreams: ["api.github.com"] };
    expect(decide(pol, "api.github.com", "GET").allow).toBe(true);
  });

  test("the deny-list wins even when the host is also allow-listed", () => {
    const pol: Policy = {
      name: "p",
      allow_upstreams: ["metadata.google.internal"],
      deny_upstreams: ["metadata.google.internal"],
    };
    expect(decide(pol, "metadata.google.internal", "GET").allow).toBe(false);
  });

  test("a per-method restriction allows a listed method", () => {
    const pol: Policy = {
      name: "p",
      allow_upstreams: ["api.github.com"],
      methods: { "api.github.com": ["GET"] },
    };
    expect(decide(pol, "api.github.com", "GET").allow).toBe(true);
  });

  test("a per-method restriction blocks an unlisted method", () => {
    const pol: Policy = {
      name: "p",
      allow_upstreams: ["api.github.com"],
      methods: { "api.github.com": ["GET"] },
    };
    expect(decide(pol, "api.github.com", "POST").allow).toBe(false);
  });

  test("an unlisted host is denied by default when the allow-list is non-empty", () => {
    const pol: Policy = { name: "p", allow_upstreams: ["api.github.com"] };
    expect(decide(pol, "unlisted.example", "GET").allow).toBe(false);
  });

  test("a '.suffix' allow rule matches a subdomain", () => {
    const pol: Policy = { name: "p", allow_upstreams: [".github.com"] };
    expect(decide(pol, "api.github.com", "GET").allow).toBe(true);
  });

  test("a '.suffix' allow rule does not match an unrelated host", () => {
    const pol: Policy = { name: "p", allow_upstreams: [".github.com"] };
    expect(decide(pol, "evil.example", "GET").allow).toBe(false);
  });
});

describe("dryRun", () => {
  test("replays events over a policy and aggregates the denied ones", () => {
    const t = new Date().toISOString();
    const pol: Policy = {
      name: "p",
      allow_upstreams: ["api.github.com"],
      deny_upstreams: ["metadata.google.internal"],
      methods: { "api.github.com": ["GET"] },
    };
    const events: Event[] = [
      { seq: 4, time: t, pod: "a", kind: "request", upstream: "api.github.com", method: "POST" }, // method blocked
      { seq: 3, time: t, pod: "a", kind: "request", upstream: "api.github.com", method: "GET" },  // allowed
      { seq: 2, time: t, pod: "a", kind: "request", upstream: "metadata.google.internal", method: "GET" }, // deny-list
      { seq: 1, time: t, pod: "a", kind: "request", upstream: "unlisted.example", method: "GET" }, // default-deny
    ];

    const result = dryRun(pol, events);

    expect(result.total).toBe(4);
    expect(result.denied).toBe(3);
    expect(result.rows).toHaveLength(3);
    expect(result.rows.map((r) => r.upstream)).toEqual(
      expect.arrayContaining(["api.github.com", "metadata.google.internal", "unlisted.example"]),
    );
  });
});
