import type { Policy } from "./types";
export declare function PolicyList({ policies, selectedName, loading, usage, hrefFor, newHref, linkTo, defaultName }: {
    policies: Policy[];
    selectedName?: string;
    loading: boolean;
    usage: (name: string) => number;
    hrefFor: (name: string) => string;
    newHref: string;
    linkTo: (href: string) => (e: MouseEvent) => void;
    defaultName?: string;
}): import("preact").JSX.Element;
