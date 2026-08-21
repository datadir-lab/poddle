import type { Toast } from "./types";
export declare function ToastHost({ toasts, onDismiss, href, linkTo }: {
    toasts: Toast[];
    onDismiss: (id: number) => void;
    href: (t: Toast) => string;
    linkTo: (href: string) => (e: MouseEvent) => void;
}): import("preact").JSX.Element | null;
