# Design — `src/web` marketing landing (v1)

**Date:** 2026-08-16
**Status:** Approved for planning
**Branch / worktree:** `worktree-web+scaffold`
**Topic:** First slice of `src/web` — the marketing landing page, site shell, and shared design tokens.

---

## 1. Context

`poddle` is a Go CLI for self-hostable, secret-safe dev sandboxes for coding agents.
It has no web presence yet (`src/web` does not exist). The roadmap parks a broad
"Cloud UI" in Phase 4; this work brings a **web surface forward**, starting with
marketing.

`src/web` will eventually host **three surfaces** — marketing site, docs, and the
authenticated dashboard. They differ on audience (public vs authenticated), content
model (static vs live/API-driven), deploy target, and change cadence, but must share
one brand/design system. We build **one surface at a time**, each with its own spec.

## 2. Goal & scope

**Goal:** ship a single, excellent **marketing landing page** plus the **site shell**
and **shared design tokens** that docs and dashboard will inherit — buildable and
previewable locally and in CI.

**In scope (v1):**
- Astro static site scaffold under `src/web/site/`.
- One landing page (`index.astro`) with the sections in §6.
- Site shell: `Base` layout, `Nav`, `Footer`.
- Shared design tokens (source of truth) + the "Literary High-Contrast" look.
- Build tooling, Taskfile targets, a Woodpecker web pipeline, gitignore entries.
- Right-sized verification (`astro check` + a build smoke test).

**Out of scope (v1), dirs reserved but not built:**
- Docs site (content collections / Starlight) — future slice.
- Dashboard (`src/web/dashboard/`) — future slice, `.gitkeep` only.
- Pricing / about / other marketing pages.
- Real hosting/deployment wiring, analytics, CMS, dark mode.

## 3. Decisions (brainstorm outcomes)

| Question | Decision |
|---|---|
| What is `src/web`? | Marketing + docs + dashboard (three surfaces) |
| Structure | **Grouped:** static `site/` (marketing + docs) · separate `dashboard/` · `shared/` tokens |
| First surface | **Marketing landing** |
| Static site stack | **Astro** (zero-JS default, MDX content collections for docs later) |
| v1 scope | Landing page + shell + tokens + build/CI |
| Brand direction | **"Literary High-Contrast"** — Instrument Serif (display) + Inter (body), warm bone/ink palette, deep-green accent, editorial + witty voice |

## 4. Architecture

Astro produces a static `dist/`. The entire Node toolchain lives under `src/web/` and
never interacts with `go build` / `task ci` — Go tooling and the architecture tests
only see Go packages, so non-Go directories are inert to them.

```
src/web/
  site/                       Astro static site (marketing now, docs later)
    src/
      pages/
        index.astro           the landing page
      layouts/
        Base.astro            <head>, meta/OG, fonts, Nav + <slot/> + Footer
      components/
        Nav.astro  Footer.astro  Hero.astro  Section.astro
        Feature.astro  Step.astro  Button.astro
      styles/
        global.css            reset + base element styles; imports shared tokens
      content/                reserved for docs collections (empty in v1)
    public/                   favicon, og-image, self-hosted font files
    astro.config.mjs
    package.json
    tsconfig.json
  shared/
    tokens.css                design tokens — SOURCE OF TRUTH (site imports it)
  dashboard/                  reserved — .gitkeep only in v1
```

**Shared tokens mechanism:** `shared/tokens.css` is the single source of truth.
`site` imports it from `global.css` via a relative path (`@import "../../shared/tokens.css";`),
enabled by Astro/Vite `server.fs.allow` including the `src/web` root. (Alternative if
`fs.allow` proves fiddly: a small prebuild copy step. Settle in the plan.)

## 5. Design system — "Literary High-Contrast"

### Typography
- **Display:** Instrument Serif (weight 400 + italic) — hero + section headings.
- **Body / UI:** Inter (400/500/600/700).
- **Delivery:** self-hosted via `@fontsource-variable/inter` and `@fontsource/instrument-serif`
  (no external font CDN — fits the self-host ethos and keeps the site dependency-free at runtime).

### Tokens (`shared/tokens.css`)
```css
:root {
  /* color */
  --bone:      #faf8f2;  /* page background */
  --surface:   #f2efe6;  /* raised surface / alternating section fill */
  --ink:       #16150f;  /* primary text, wordmark */
  --muted:     #4a4840;  /* secondary text */
  --faint:     #8a8577;  /* tertiary text, eyebrows, borders-on-dark */
  --accent:    #2e5d4e;  /* deep green — links, accent words, primary action */
  --accent-ink:#23473c;  /* accent hover/active */
  --border:    #e7e2d5;  /* hairlines, card borders */

  /* type */
  --font-serif: "Instrument Serif", Georgia, "Times New Roman", serif;
  --font-sans:  "Inter", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;

  /* scale (fluid) */
  --fs-hero:  clamp(2.5rem, 6vw, 4.5rem);   /* serif 400, lh 1.03, ls -0.01em */
  --fs-h2:    clamp(1.75rem, 3vw, 2.4rem);  /* serif 400 */
  --fs-h3:    1.25rem;                       /* sans 600 */
  --fs-lead:  1.1875rem;                     /* subhead */
  --fs-body:  1.0625rem;                     /* body, lh 1.62 */
  --fs-eyebrow: 0.75rem;                     /* sans 600, uppercase, ls 0.15em */

  /* layout */
  --maxw: 1080px;
  --radius-sm: 8px;
  --radius-lg: 16px;
  --space-section: clamp(4rem, 10vw, 8rem);
}
```

### Layout & feel
Left-aligned editorial, generous whitespace, `--maxw` ~1080px, high type contrast,
sparing use of the deep-green accent (links + one accent word per headline). Light
theme only in v1 (dark deferred).

### Voice
Witty, specific, confident, editorial — in the register of the reference sites
(e.g. "Every secret stays home — especially the ones you forgot you had."). Concrete
over grandiose; specifics over hype.

## 6. Landing page content

Sections, top to bottom. Copy below is drafted from `README.md` / `ROADMAP.md` and is
placeholder-but-real — to be refined during build.

1. **Nav** — `poddle` wordmark (Instrument Serif) · links: Docs, GitHub/Forgejo · primary CTA "Self-host free".
2. **Hero** — eyebrow "Self-hostable · Secretless"; headline *"A sandbox your coding agent **can't** leak out of."* (italic accent word); subhead "Spin up an isolated, reproducible pod on your own infrastructure — your agent fully authed, and not one vendor secret inside it."; CTAs "Self-host free" (primary) + "Read the docs" (ghost).
3. **Problem → solution** — "Coding agents want your keys. Don't give them any." The broker holds the real credential; the pod holds a revocable handle; an injecting gateway swaps handle → secret on the wire. Payoff line: "No vendor secret ever inside the pod."
4. **Key features** (4):
   - *Isolated, reproducible pods* — podman-backed, local or over SSH to your own host.
   - *Secretless by construction* — a broker issues revocable handles; the real secret never enters the pod.
   - *Your infra, your rules* — self-host on your machine/homelab; BYO compute, wired to Forgejo / Woodpecker.
   - *Bring your own agent* — harness registry (`claude-code` today; `codex`, `aider`, `pi`, `local` planned).
5. **How it works** (3 steps):
   1. Add an identity — `poddle identity add work`.
   2. Spin up — `poddle up --identity work` (broker starts, handle issued, pod attached).
   3. Work — the agent runs in the pod, calling out through the broker; `poddle down` revokes.
6. **Closing CTA** — "Give your agents a room of their own." · "Self-host free" / "Star on Forgejo".
7. **Footer** — link columns (Product · Resources · Project), OSS/license note, repo link, copyright.

## 7. Build, tooling, CI, deploy

- **Scripts** (`src/web/site/package.json`): `dev`, `build`, `preview`, `check` (`astro check`).
- **Taskfile:** add `web:dev`, `web:build`, `web:check`. Kept **separate** from the Go
  `task ci` so the Go and web pipelines stay independent (matches the repo's boundary ethos).
- **Woodpecker:** a web pipeline triggered on changes under `src/web/**` running
  `npm ci && npm run build && npm run check`.
- **Output:** static `dist/`. **Hosting deferred** — v1 only needs a clean build + preview.
- **Gitignore:** add `src/web/site/node_modules`, `src/web/site/dist`, `src/web/site/.astro`.

## 8. Testing / verification (right-sized)

- **Gate:** `astro check` (TypeScript + template/prop checks).
- **Build smoke:** after `npm run build`, assert `dist/index.html` contains the hero
  headline text and the primary nav + CTA links (a small Vitest test, or a grep-based
  assertion in the Taskfile). No heavy browser e2e for a static marketing page in v1.
- **Semantics/a11y:** semantic landmarks, `alt` text, keyboard-reachable nav; Lighthouse
  optional, not gated.
- Go tests and architecture tests are untouched (web is non-Go).

## 9. Boundaries & repo fit

- Non-Go dirs are ignored by `go build ./src/...`, `task ci`, and the architecture tests.
- Module path (`github.com/datadir-lab/poddle/src/...`) is unaffected.
- `.superpowers/` already added to `.gitignore` (visual-companion scratch).

## 10. Open questions (defer, don't block v1)

- **Hosting target** for `dist/` (self-hosted static host? object storage? behind their edge?) — decide when we wire deployment.
- **Docs toolchain** (Astro content collections vs Starlight) — decide when the docs slice starts.
- **Shared-tokens import** (Vite `fs.allow` vs copy step) — settle in the implementation plan.
- **Dark mode**, analytics — deferred; if analytics ever, prefer privacy-friendly/self-hosted.

## 11. Future surfaces (context, not this slice)

- **Docs** — reuse the `site/` Astro toolchain; content collections under `src/web/site/src/content/`.
- **Dashboard** — separate app under `src/web/dashboard/`, authenticated, integrates with the
  broker/backend; stack chosen when Phase-1/2 backend surfaces stabilize. Inherits `shared/` tokens.
