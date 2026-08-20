import type { Event, Grouped, Stats } from "./types";
export declare function summarise(events: Event[]): Stats;
export declare function group(events: Event[], decisions: string[]): Grouped[];
export declare const cap1: (s: string) => string;
export declare const humanKind: (k: string) => string;
export declare function relTime(iso: string): string;
export declare const threshTone: (v: number) => "hot" | "warm" | "cool";
