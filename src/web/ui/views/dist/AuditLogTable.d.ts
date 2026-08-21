import type { Event } from "./types";
export declare function AuditLogTable({ events, initialPod, initialQ, loading, onExport }: {
    events: Event[];
    initialPod?: string;
    initialQ?: string;
    loading: boolean;
    onExport?: (rows: Event[]) => void;
}): import("preact").JSX.Element;
