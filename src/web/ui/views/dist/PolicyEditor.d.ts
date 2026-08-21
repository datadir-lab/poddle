import type { ComponentChildren } from "preact";
import type { Event, Policy, PolicyTemplate } from "./types";
export declare function PolicyEditor({ policy, events, scopePods, onSave, onDelete, hint, templates, isSaved, isDefault, onSetDefault, onDuplicate }: {
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
    isSaved?: boolean;
    isDefault?: boolean;
    onSetDefault?: (name: string) => void;
    onDuplicate?: (p: Policy) => void;
}): import("preact").JSX.Element;
