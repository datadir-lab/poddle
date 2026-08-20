import type { Event, Pod } from "./types";
export declare function PodDetailPanel({ name, pod, hist, events, onBack, onPolicyClick }: {
    name: string;
    pod?: Pod;
    hist: {
        cpu: number[];
        mem: number[];
    };
    events: Event[];
    onBack: (e: MouseEvent) => void;
    onPolicyClick?: (e: MouseEvent) => void;
}): import("preact").JSX.Element;
