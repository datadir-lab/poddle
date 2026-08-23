import type { Event } from "./types";
export declare function toCsv(rows: Event[], opts?: {
    multiHost?: boolean;
}): string;
export declare function downloadCsv(name: string, rows: Event[], opts?: {
    multiHost?: boolean;
}): void;
