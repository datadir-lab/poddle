import type { ComponentChildren } from "preact";
import type { Pod, Hist } from "./types";
export declare function PodFleetTable({ pods, hist, onPod, emptyState }: {
    pods: Pod[];
    hist: Hist;
    onPod: (pod: string) => void;
    emptyState: ComponentChildren;
}): import("preact").JSX.Element;
