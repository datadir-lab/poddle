import type { Cmd } from "./types";
export declare function CommandPalette({ open, onClose, commands }: {
    open: boolean;
    onClose: () => void;
    commands: Cmd[];
}): import("preact").JSX.Element | null;
