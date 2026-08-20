import type { Policy } from "./types";
export declare function PolicyList({ policies, selectedName, hrefFor, newHref, linkTo }: {
    policies: Policy[];
    selectedName?: string;
    hrefFor: (name: string) => string;
    newHref: string;
    linkTo: (href: string) => (e: MouseEvent) => void;
}): import("preact").JSX.Element;
