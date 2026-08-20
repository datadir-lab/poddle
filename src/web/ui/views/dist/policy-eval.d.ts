import type { AllowRow, DryRow, Event, Policy } from "./types";
export declare function matchHost(host: string, patterns: string[]): boolean;
export declare function methodsFor(methods: Record<string, string[]> | undefined, host: string): string[] | null;
export declare function decide(pol: Policy, host: string, method: string): {
    decision: "allow" | "deny" | "redact" | "block";
    reason: string;
};
export declare function dryRun(pol: Policy, events: Event[]): DryRow[];
export declare function toRows(p: Policy): AllowRow[];
