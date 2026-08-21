import type { Policy } from "./types";
export declare function PolicyList({ policies, selectedName, loading, usage, hrefFor, newHref, linkTo }: {
    policies: Policy[];
    selectedName?: string;
    loading: boolean;
    usage: (name: string) => number;
    hrefFor: (name: string) => string;
    newHref: string;
    linkTo: (href: string) => (e: MouseEvent) => void;
}): import("preact").JSX.Element;
