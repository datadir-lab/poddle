import type { ComponentChildren } from "preact";
import type { Event, Policy } from "./types";
export declare function PolicyEditor({ policy, events, scopePods, onSave, onDelete, hint }: {
    policy: Policy;
    events: Event[];
    scopePods: string[];
    onSave: (p: Policy) => Promise<{
        ok: boolean;
        error?: string;
    }>;
    onDelete: () => Promise<void>;
    hint?: (name: string) => ComponentChildren;
}): import("preact").JSX.Element;
