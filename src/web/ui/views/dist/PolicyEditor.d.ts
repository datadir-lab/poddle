import type { ComponentChildren } from "preact";
import type { Policy } from "./types";
export declare function PolicyEditor({ policy, onSave, onDelete, hint }: {
    policy: Policy;
    onSave: (p: Policy) => Promise<{
        ok: boolean;
        error?: string;
    }>;
    onDelete: () => Promise<void>;
    hint?: ComponentChildren;
}): import("preact").JSX.Element;
