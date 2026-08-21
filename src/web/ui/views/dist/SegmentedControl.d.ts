import type { SegOption } from "./types";
export declare function SegmentedControl({ value, options, onChange, ariaLabel }: {
    value: string;
    options: SegOption[];
    onChange: (v: string) => void;
    ariaLabel: string;
}): import("preact").JSX.Element;
