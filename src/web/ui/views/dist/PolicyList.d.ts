import type { Policy } from "./types";
export declare function PolicyList({ policies, selectedName, onSelect, onNew }: {
    policies: Policy[];
    selectedName?: string;
    onSelect: (name: string) => void;
    onNew: () => void;
}): import("preact").JSX.Element;
