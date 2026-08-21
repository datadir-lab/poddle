import type { ComponentChildren } from "preact";
import type { Event, Policy, PolicyTemplate } from "./types";
export declare function PolicyEditor({ policy, events, scopePods, onSave, onDelete, hint, templates, isDefault, onSetDefault }: {
    policy: Policy;
    events: Event[];
    scopePods: string[];
    onSave: (p: Policy) => Promise<{
        ok: boolean;
        error?: string;
    }>;
    onDelete: () => Promise<void>;
    hint?: (name: string) => ComponentChildren;
    templates?: PolicyTemplate[];
    isDefault?: boolean;
    onSetDefault?: (name: string) => void;
}): import("preact").JSX.Element;
