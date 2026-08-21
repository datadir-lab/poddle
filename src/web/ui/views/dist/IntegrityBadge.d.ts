import type { Verify } from "./types";
export declare function IntegrityBadge({ v, compact, href, onClick }: {
    v: Verify;
    compact?: boolean;
    href?: string;
    onClick?: (e: MouseEvent) => void;
}): import("preact").JSX.Element;
