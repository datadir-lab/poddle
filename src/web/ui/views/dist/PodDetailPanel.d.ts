import type { ComponentChildren } from "preact";
import type { Event, Pod, Policy } from "./types";
export declare function PodDetailPanel({ name, pod, hist, events, loading, backHref, onBack, policyHref, onPolicyClick, controls, onSuggestPolicy }: {
    name: string;
    pod?: Pod;
    hist: {
        cpu: number[];
        mem: number[];
    };
    events: Event[];
    loading: boolean;
    backHref: string;
    onBack: (e: MouseEvent) => void;
    policyHref?: string;
    onPolicyClick?: (e: MouseEvent) => void;
    controls?: ComponentChildren;
    onSuggestPolicy?: (p: Policy) => void;
}): import("preact").JSX.Element;
