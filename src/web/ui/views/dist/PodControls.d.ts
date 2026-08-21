import type { Pod, Policy } from "./types";
export declare function PodControls({ pod, policies, onBind, onRevoke, onRebound }: {
    pod: Pod;
    policies: Policy[];
    onBind: (policyName: string) => Promise<{
        ok: boolean;
        msg: string;
    }>;
    onRevoke: () => Promise<{
        ok: boolean;
        msg: string;
    }>;
    onRebound?: (name: string) => void;
}): import("preact").JSX.Element;
