import { useEffect, useRef, useState } from "preact/hooks";
import type { Cmd } from "./types";
import { Icon } from "./Icon";

// CommandPalette is a ⌘K/Ctrl-K launcher: fuzzy-jump to any view, pod, policy,
// or destination. The container builds `commands` (fetching pods/policies on
// open, deriving destinations from the audit stream already in memory) — this
// component only filters, highlights, and activates them.
export function CommandPalette({ open, onClose, commands }: {
  open: boolean;
  onClose: () => void;
  commands: Cmd[];
}) {
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    setQ(""); setSel(0);
    const id = setTimeout(() => inputRef.current?.focus(), 0);
    return () => clearTimeout(id);
  }, [open]);

  const s = q.toLowerCase();
  const shown = q ? commands.filter((c) => c.label.toLowerCase().includes(s) || c.hint.includes(s)) : commands;
  const selClamped = Math.min(sel, Math.max(0, shown.length - 1));

  if (!open) return null;

  const run = (c: Cmd) => { onClose(); c.run(); };
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setSel((i) => Math.min(i + 1, shown.length - 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setSel((i) => Math.max(i - 1, 0)); }
    else if (e.key === "Enter") { e.preventDefault(); if (shown[selClamped]) run(shown[selClamped]); }
    else if (e.key === "Escape") { e.preventDefault(); onClose(); }
  };

  return (
    <div class="cmdk" role="dialog" aria-modal="true" aria-label="Command palette" onClick={onClose}>
      <div class="cmdk__panel" onClick={(e) => e.stopPropagation()}>
        <div class="cmdk__search">
          <span class="cmdk__searchic" aria-hidden="true"><Icon name="search" size={16} /></span>
          <input ref={inputRef} class="cmdk__input" placeholder="Jump to a view, pod, policy, or destination…"
            value={q} aria-label="Command palette search"
            onInput={(e) => { setQ((e.target as HTMLInputElement).value); setSel(0); }} onKeyDown={onKey} />
        </div>
        <ul class="cmdk__list">
          {shown.length === 0 && <li class="cmdk__empty">No matches.</li>}
          {shown.slice(0, 40).map((c, i) => (
            <li key={c.id}>
              <button type="button" class={"cmdk__item" + (i === selClamped ? " on" : "")}
                onMouseEnter={() => setSel(i)} onClick={() => run(c)}>
                <span class="cmdk__ic" aria-hidden="true"><Icon name={c.icon} size={15} /></span>
                <span class="cmdk__lb">{c.label}</span>
                <span class="cmdk__hint">{c.hint}</span>
              </button>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
