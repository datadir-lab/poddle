import type { Toast } from "./types";
import { Icon } from "./Icon";
import { DecisionBadge } from "./DecisionBadge";

// ToastHost surfaces live denials/blocks the moment they stream in, so the
// console tells you rather than waiting to be checked. The container owns the
// live subscription and dismiss timers; `href` builds the audit-link target
// and `linkTo` is the consumer's modifier-aware click-handler factory (a
// plain left-click does SPA nav, ⌘/Ctrl/Shift/middle-click falls through to
// the browser's native "open in new tab") — no router logic lives in this
// file.
export function ToastHost({ toasts, onDismiss, href, linkTo }: {
  toasts: Toast[];
  onDismiss: (id: number) => void;
  href: (t: Toast) => string;
  linkTo: (href: string) => (e: MouseEvent) => void;
}) {
  if (toasts.length === 0) return null;
  return (
    <div class="toasts" role="region" aria-label="Live alerts">
      {toasts.map((t) => {
        const to = href(t);
        return (
          <div key={t.id} class="toast" role="status">
            <span class="toast__ic" aria-hidden="true"><Icon name={t.decision === "block" ? "octagon" : "ban"} size={16} /></span>
            <div class="toast__body">
              <div class="toast__title"><span class="c-pod">{t.pod}</span> <DecisionBadge decision={t.decision} /></div>
              <a class="toast__link c-mono" href={to} onClick={linkTo(to)}>{t.upstream || "egress"}</a>
            </div>
            <button type="button" class="toast__close" aria-label="Dismiss alert" onClick={() => onDismiss(t.id)}>×</button>
          </div>
        );
      })}
    </div>
  );
}
