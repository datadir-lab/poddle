// ---- icons ----
// A small inline-SVG set (lucide-style, matching the theme-toggle glyphs) so the
// nav, stat cards, and chart legends carry meaning by shape as well as label -
// and it keeps the bundle dependency-free for go:embed. Each entry is a render
// fn (not a shared vnode) so the same icon can be drawn in many places safely.
export const ICONS: Record<string, () => any> = {
  overview: () => (<><rect x="3" y="3" width="7" height="7" rx="1.4" /><rect x="14" y="3" width="7" height="7" rx="1.4" /><rect x="14" y="14" width="7" height="7" rx="1.4" /><rect x="3" y="14" width="7" height="7" rx="1.4" /></>),
  pods: () => (<><path d="M21 8v8a2 2 0 0 1-1 1.73l-7 4a2 2 0 0 1-2 0l-7-4A2 2 0 0 1 3 16V8a2 2 0 0 1 1-1.73l7-4a2 2 0 0 1 2 0l7 4A2 2 0 0 1 21 8Z" /><path d="m3.3 7 8.7 5 8.7-5" /><path d="M12 22V12" /></>),
  audit: () => (<path d="M22 12h-4l-3 9L9 3l-3 9H2" />),
  policies: () => (<><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67 0C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1Z" /><path d="m9 12 2 2 4-4" /></>),
  globe: () => (<><circle cx="12" cy="12" r="10" /><path d="M12 2a15 15 0 0 1 0 20 15 15 0 0 1 0-20M2 12h20" /></>),
  eyeoff: () => (<><path d="M9.88 9.88a3 3 0 1 0 4.24 4.24" /><path d="M10.73 5.08A11 11 0 0 1 12 5c7 0 10 7 10 7a13 13 0 0 1-1.67 2.68" /><path d="M6.61 6.61A13 13 0 0 0 2 12s3 7 10 7a11 11 0 0 0 5.39-1.39" /><line x1="2" y1="2" x2="22" y2="22" /></>),
  ban: () => (<><circle cx="12" cy="12" r="10" /><path d="m4.9 4.9 14.2 14.2" /></>),
  check: () => (<path d="M20 6 9 17l-5-5" />),
  octagon: () => (<><polygon points="7.86 2 16.14 2 22 7.86 22 16.14 16.14 22 7.86 22 2 16.14 2 7.86" /><line x1="15" y1="9" x2="9" y2="15" /><line x1="9" y1="9" x2="15" y2="15" /></>),
  panel: () => (<><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="9" y1="3" x2="9" y2="21" /></>),
  search: () => (<><circle cx="11" cy="11" r="7" /><line x1="21" y1="21" x2="16.65" y2="16.65" /></>),
  theme: () => (<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" />),
};
export function Icon({ name, size = 16 }: { name: string; size?: number }) {
  const draw = ICONS[name];
  if (!draw) return null;
  return (
    <svg class="icon" width={size} height={size} viewBox="0 0 24 24" fill="none"
      stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      {draw()}
    </svg>
  );
}
