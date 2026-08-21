import { describe, expect, it } from "vitest";
import { absTime } from "./aggregate";

// absTime renders an absolute wall-clock time (24h), the date-prefix only for
// days other than today. Building the fixtures from a local Date and round-
// tripping through toISOString keeps the assertions timezone-independent.
describe("absTime", () => {
  it("shows HH:MM:SS for a time earlier today", () => {
    const t = new Date();
    t.setHours(9, 5, 7, 0);
    expect(absTime(t.toISOString())).toBe("09:05:07");
  });

  it("drops the seconds when withSeconds is false (charts)", () => {
    const t = new Date();
    t.setHours(14, 30, 45, 0);
    expect(absTime(t.toISOString(), false)).toBe("14:30");
  });

  it("prefixes the date for a day that is not today", () => {
    const t = new Date();
    t.setFullYear(t.getFullYear() - 1);
    t.setHours(8, 15, 0, 0);
    expect(absTime(t.toISOString())).toMatch(/^[A-Za-z]{3}\s+\d+\s+08:15:00$/);
  });

  it("returns empty for an unparseable input", () => {
    expect(absTime("not-a-date")).toBe("");
  });
});
