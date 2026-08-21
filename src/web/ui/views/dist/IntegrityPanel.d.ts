import type { Verify } from "./types";
export declare function IntegrityPanel({ verify, checkedAt, recheck, count }: {
    verify: Verify;
    checkedAt: number;
    recheck: () => void;
    count: number;
}): import("preact").JSX.Element;
