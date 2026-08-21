import type { Event } from "./types";
export type TBucket = {
    t0: number;
    req: number;
    intervened: number;
};
export declare function bucketEvents(events: Event[], n?: number): TBucket[];
