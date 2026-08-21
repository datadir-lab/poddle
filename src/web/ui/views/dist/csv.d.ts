import type { Event } from "./types";
export declare function toCsv(rows: Event[]): string;
export declare function downloadCsv(name: string, rows: Event[]): void;
