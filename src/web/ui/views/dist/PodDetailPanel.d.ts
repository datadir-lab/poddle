import type { Event, Pod } from "./types";
export declare function PodDetailPanel({ name, pod, hist, events, backHref, onBack, policyHref, onPolicyClick }: {
    name: string;
    pod?: Pod;
    hist: {
        cpu: number[];
        mem: number[];
    };
    events: Event[];
    backHref: string;
    onBack: (e: MouseEvent) => void;
    policyHref?: string;
    onPolicyClick?: (e: MouseEvent) => void;
}): import("preact").JSX.Element;
