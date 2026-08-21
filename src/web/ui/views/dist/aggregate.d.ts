import type { Dest, Event, Grouped, Stats } from "./types";
export declare const secretsFrom: (detail?: string) => number;
export declare function summarise(events: Event[]): Stats;
export declare function group(events: Event[], decisions: string[]): Grouped[];
export declare const cap1: (s: string) => string;
export declare const humanKind: (k: string) => string;
export declare function relTime(iso: string): string;
export declare function absTime(iso: string, withSeconds?: boolean): string;
export declare const threshTone: (v: number) => "hot" | "warm" | "cool";
export declare const DECISIONS: readonly [{
    readonly key: "allow";
    readonly label: "Allow";
    readonly icon: "check";
}, {
    readonly key: "redact";
    readonly label: "Redact";
    readonly icon: "eyeoff";
}, {
    readonly key: "deny";
    readonly label: "Deny";
    readonly icon: "ban";
}, {
    readonly key: "block";
    readonly label: "Block";
    readonly icon: "octagon";
}];
export declare function decisionCounts(events: Event[]): Record<string, number>;
export declare function destinations(events: Event[]): Dest[];
export declare function rowKey(onClick: () => void): (e: KeyboardEvent) => void;
