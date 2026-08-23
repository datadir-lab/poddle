import type { Event, Policy } from "./types";
export declare function ActivityProfile({ podName, events, onSuggestPolicy }: {
    podName: string;
    events: Event[];
    onSuggestPolicy: (p: Policy) => void;
}): import("preact").JSX.Element;
