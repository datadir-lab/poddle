import { describe, expect, it } from "vitest";
import { decide } from "./policy-eval";
import type { Policy } from "./types";

// The dry-run's decide() is a faithful port of Go policy.Decide, so the "*"
// catch-all method rule must resolve the same way (most specific first).
describe("decide — global (*) method rule", () => {
  const p: Policy = {
    name: "ro",
    allow_upstreams: [".example.com", "api.github.com"],
    methods: { "*": ["GET", "HEAD"], "api.github.com": ["GET", "POST"] },
  };

  it("the catch-all restricts methods on any host without a specific rule", () => {
    expect(decide(p, "read.example.com", "GET").allow).toBe(true);
    expect(decide(p, "read.example.com", "HEAD").allow).toBe(true);
    expect(decide(p, "read.example.com", "POST").allow).toBe(false);
  });

  it("a specific host rule overrides the catch-all", () => {
    expect(decide(p, "api.github.com", "POST").allow).toBe(true);
  });

  it("CONNECT bypasses method rules (the method is encrypted)", () => {
    expect(decide(p, "read.example.com", "CONNECT").allow).toBe(true);
  });
});
