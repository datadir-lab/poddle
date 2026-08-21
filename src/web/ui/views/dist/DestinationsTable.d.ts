import type { Dest } from "./types";
export declare function DestinationsTable({ dests, loading, onSelect }: {
    dests: Dest[];
    loading: boolean;
    onSelect: (upstream: string) => void;
}): import("preact").JSX.Element;
