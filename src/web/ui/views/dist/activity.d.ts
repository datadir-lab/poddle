import type { Event, Policy } from "./types";
export type Category = {
    key: string;
    label: string;
    patterns: string[];
};
export declare const CATEGORIES: Category[];
export declare function classify(host: string): string;
export type CategoryRollup = {
    key: string;
    label: string;
    total: number;
    hosts: string[];
    methods: string[];
    allow: number;
    redact: number;
    deny: number;
    block: number;
};
export declare function categorize(events: Event[]): CategoryRollup[];
export declare function suggestPolicy(events: Event[], name: string): Policy;
