# Web Marketing Landing — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship an Astro static marketing landing page for poddle under `src/web/site/`, plus the site shell and shared design tokens, in the "Literary High-Contrast" brand direction.

**Architecture:** A self-contained Astro static site under `src/web/site/` (Node toolchain, inert to Go tooling). Design tokens live in `src/web/shared/tokens.css` as the single source of truth and are imported by the site. Components are TDD'd with Vitest + Astro's Container API (render `.astro` components to strings and assert content); `astro check` + `astro build` are the integration gates, wired into a Taskfile namespace and a change-scoped Woodpecker pipeline.

**Tech Stack:** Astro 5 (static output), Inter + Instrument Serif via `@fontsource`, TypeScript (`astro check`), Vitest 3 + `astro/container`, Task (Taskfile), Woodpecker CI.

## Global Constraints

- All web code lives under `src/web/`. It must not affect `go build ./src/...`, `task ci`, `task test`, or the architecture tests — Go tooling and the arch tests only see Go packages, so non-Go dirs are inert.
- Node toolchain via **npm**; Node ≥ 20 (CI uses `node:22`). Commit `package-lock.json` so CI can `npm ci`.
- Astro **static** output — no SSR adapter.
- Fonts are **self-hosted** via `@fontsource` packages — no external font CDN at runtime.
- Design tokens are the single source of truth in `src/web/shared/tokens.css`; the site imports them (never redefine token values inside `site/`).
- Brand = **"Literary High-Contrast"**: display font **Instrument Serif** (400 + italic), body/UI font **Inter**. Exact palette (verbatim): bone `#faf8f2`, surface `#f2efe6`, ink `#16150f`, muted `#4a4840`, faint `#8a8577`, accent `#2e5d4e`, accent-ink `#23473c`, border `#e7e2d5`. **Light theme only** (no dark mode).
- Voice: witty, specific, editorial. Hero copy is used **verbatim** as written in these tasks.
- Source/repo link everywhere: `https://github.com/datadir-lab/poddle` (Forgejo).
- Commit after each task with a `feat(web): …` / `chore(web): …` message.

---

### Task 1: Scaffold the Astro `site/` project + shared tokens

Stand up the Astro project so `build`, `check`, and `test` are all green with a placeholder page. Establishes the toolchain the rest of the plan builds on.

**Files:**
- Create: `src/web/shared/tokens.css`
- Create: `src/web/dashboard/.gitkeep`
- Create: `src/web/site/package.json`
- Create: `src/web/site/astro.config.mjs`
- Create: `src/web/site/tsconfig.json`
- Create: `src/web/site/vitest.config.ts`
- Create: `src/web/site/src/styles/global.css`
- Create: `src/web/site/src/pages/index.astro` (temporary placeholder; replaced in Task 2)
- Create: `src/web/site/public/favicon.svg`
- Modify: `.gitignore` (repo root)

**Interfaces:**
- Produces: the `@poddle/site` Astro project; token CSS variables (see values below) available globally via `global.css`; npm scripts `dev`/`build`/`preview`/`check`/`test`.

- [ ] **Step 1: Create the design tokens (source of truth)**

`src/web/shared/tokens.css`:
```css
/* poddle design tokens — single source of truth. Imported by src/web/site. */
:root {
  /* color */
  --bone: #faf8f2;        /* page background */
  --surface: #f2efe6;     /* raised surface / alternating section fill */
  --ink: #16150f;         /* primary text, wordmark */
  --muted: #4a4840;       /* secondary text */
  --faint: #8a8577;       /* tertiary text, eyebrows */
  --accent: #2e5d4e;      /* deep green — links, accent words, primary action */
  --accent-ink: #23473c;  /* accent hover/active */
  --border: #e7e2d5;      /* hairlines, card borders */

  /* type */
  --font-serif: "Instrument Serif", Georgia, "Times New Roman", serif;
  --font-sans: "Inter Variable", "Inter", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;

  /* fluid scale */
  --fs-hero: clamp(2.5rem, 6vw, 4.5rem);
  --fs-h2: clamp(1.75rem, 3vw, 2.4rem);
  --fs-h3: 1.25rem;
  --fs-lead: 1.1875rem;
  --fs-body: 1.0625rem;
  --fs-eyebrow: 0.75rem;

  /* layout */
  --maxw: 1080px;
  --radius-sm: 8px;
  --radius-lg: 16px;
  --space-section: clamp(4rem, 10vw, 8rem);
}
```

- [ ] **Step 2: Create `src/web/dashboard/.gitkeep`** (reserve the dir)

`src/web/dashboard/.gitkeep`:
```
# Reserved for the dashboard surface (future slice). See docs/superpowers/specs/2026-08-16-web-marketing-landing-design.md
```

- [ ] **Step 3: Create `src/web/site/package.json`**

```json
{
  "name": "@poddle/site",
  "type": "module",
  "version": "0.0.0",
  "private": true,
  "scripts": {
    "dev": "astro dev",
    "build": "astro build",
    "preview": "astro preview",
    "check": "astro check",
    "test": "vitest run"
  },
  "dependencies": {
    "astro": "^5.0.0",
    "@fontsource-variable/inter": "^5.0.0",
    "@fontsource/instrument-serif": "^5.0.0"
  },
  "devDependencies": {
    "@astrojs/check": "^0.9.0",
    "typescript": "^5.6.0",
    "vitest": "^3.0.0"
  }
}
```

- [ ] **Step 4: Create `src/web/site/astro.config.mjs`**

`fs.allow` lets the dev server read `../shared/tokens.css` (outside the site root). Build resolves it regardless.
```js
import { defineConfig } from 'astro/config';
import { fileURLToPath } from 'node:url';

// Allow importing shared tokens from ../shared during `astro dev`.
const webRoot = fileURLToPath(new URL('..', import.meta.url));

export default defineConfig({
  site: 'https://poddle.dev', // placeholder canonical; revisit when hosting is chosen
  vite: {
    server: { fs: { allow: [webRoot] } },
  },
});
```

- [ ] **Step 5: Create `src/web/site/tsconfig.json`**

```json
{
  "extends": "astro/tsconfigs/strict",
  "include": [".astro", "src", "tests"],
  "exclude": ["dist"]
}
```

- [ ] **Step 6: Create `src/web/site/vitest.config.ts`**

`getViteConfig` wires Astro's transforms into Vitest so `.astro` components (and their CSS imports) work in tests.
```ts
import { getViteConfig } from 'astro/config';

export default getViteConfig({
  test: {
    include: ['tests/**/*.test.ts'],
    passWithNoTests: true,
  },
});
```

- [ ] **Step 7: Create `src/web/site/src/styles/global.css`**

```css
@import "../../../shared/tokens.css";

*, *::before, *::after { box-sizing: border-box; }

html { -webkit-text-size-adjust: 100%; }

body {
  margin: 0;
  background: var(--bone);
  color: var(--ink);
  font-family: var(--font-sans);
  font-size: var(--fs-body);
  line-height: 1.62;
  -webkit-font-smoothing: antialiased;
}

a { color: var(--accent); text-decoration: none; }
a:hover { color: var(--accent-ink); }

h1, h2, h3 { font-family: var(--font-serif); font-weight: 400; line-height: 1.05; letter-spacing: -0.01em; }

img { max-width: 100%; display: block; }

.container { width: 100%; max-width: var(--maxw); margin-inline: auto; padding-inline: 1.5rem; }

.eyebrow {
  font-family: var(--font-sans);
  font-size: var(--fs-eyebrow);
  font-weight: 600;
  letter-spacing: 0.15em;
  text-transform: uppercase;
  color: var(--faint);
}

.btn {
  display: inline-block;
  font-family: var(--font-sans);
  font-size: 0.9375rem;
  font-weight: 600;
  padding: 0.7rem 1.25rem;
  border-radius: var(--radius-sm);
  border: 1px solid transparent;
  cursor: pointer;
  transition: background 0.12s, color 0.12s, border-color 0.12s;
}
.btn--primary { background: var(--ink); color: var(--bone); }
.btn--primary:hover { background: #000; color: var(--bone); }
.btn--ghost { background: transparent; color: var(--ink); border-color: var(--border); }
.btn--ghost:hover { border-color: var(--ink); color: var(--ink); }
```

- [ ] **Step 8: Create the placeholder `src/web/site/src/pages/index.astro`**

Temporary — Task 2 replaces this with the `Base` layout.
```astro
---
import '../styles/global.css';
---
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>poddle</title>
  </head>
  <body>
    <main class="container"><h1>poddle</h1></main>
  </body>
</html>
```

- [ ] **Step 9: Create `src/web/site/public/favicon.svg`**

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <rect width="32" height="32" rx="8" fill="#16150f"/>
  <text x="16" y="23" font-family="Georgia, serif" font-size="20" fill="#faf8f2" text-anchor="middle">p</text>
</svg>
```

- [ ] **Step 10: Add web build artifacts to root `.gitignore`**

Append to `.gitignore`:
```
# Web (Astro) build + deps
src/web/site/node_modules/
src/web/site/dist/
src/web/site/.astro/
```

- [ ] **Step 11: Install dependencies**

Run: `cd src/web/site && npm install`
Expected: dependencies install; `package-lock.json` is created.

- [ ] **Step 12: Verify build, check, and test all pass**

Run: `cd src/web/site && npm run build && npm run check && npm test`
Expected: build completes and creates `dist/index.html`; `astro check` reports 0 errors; Vitest reports no test files and exits 0 (`passWithNoTests`).

- [ ] **Step 13: Commit**

```bash
git add src/web .gitignore
git commit -m "chore(web): scaffold Astro site project + shared design tokens"
```

---

### Task 2: Base layout (head, meta, fonts, tokens)

**Files:**
- Create: `src/web/site/src/layouts/Base.astro`
- Modify: `src/web/site/src/pages/index.astro`
- Create: `src/web/site/tests/base.test.ts`

**Interfaces:**
- Produces: `Base.astro` with `Props { title: string; description?: string }`, rendering `<html lang="en">`, `<head>` (charset, viewport, favicon, title, description, OpenGraph), a `<main>` wrapping a default `<slot />`, and self-hosted fonts + `global.css`. Later tasks pass page content as children of `Base`.

- [ ] **Step 1: Write the failing test**

`src/web/site/tests/base.test.ts`:
```ts
import { experimental_AstroContainer as AstroContainer } from 'astro/container';
import { expect, test } from 'vitest';
import Base from '../src/layouts/Base.astro';

test('Base renders html lang, title, and slotted content', async () => {
  const container = await AstroContainer.create();
  const html = await container.renderToString(Base, {
    props: { title: 'poddle — secret-safe sandboxes' },
    slots: { default: '<p>SLOT_MARKER</p>' },
  });
  expect(html).toContain('<html lang="en"');
  expect(html).toContain('<title>poddle — secret-safe sandboxes</title>');
  expect(html).toContain('SLOT_MARKER');
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd src/web/site && npx vitest run tests/base.test.ts`
Expected: FAIL — cannot resolve `../src/layouts/Base.astro`.

- [ ] **Step 3: Implement `Base.astro`**

`src/web/site/src/layouts/Base.astro`:
```astro
---
import '@fontsource-variable/inter';
import '@fontsource/instrument-serif';
import '@fontsource/instrument-serif/400-italic.css';
import '../styles/global.css';

interface Props {
  title: string;
  description?: string;
}
const {
  title,
  description = 'Self-hostable, secret-safe dev sandboxes for coding agents.',
} = Astro.props;
---
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <link rel="icon" href="/favicon.svg" type="image/svg+xml" />
    <title>{title}</title>
    <meta name="description" content={description} />
    <meta property="og:title" content={title} />
    <meta property="og:description" content={description} />
    <meta property="og:type" content="website" />
  </head>
  <body>
    <main><slot /></main>
  </body>
</html>
```

- [ ] **Step 4: Convert `index.astro` to use `Base`**

Replace `src/web/site/src/pages/index.astro` with:
```astro
---
import Base from '../layouts/Base.astro';
---
<Base title="poddle — secret-safe sandboxes for coding agents">
  <section class="container"><h1>poddle</h1></section>
</Base>
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd src/web/site && npx vitest run tests/base.test.ts`
Expected: PASS.

- [ ] **Step 6: Verify build + check**

Run: `cd src/web/site && npm run build && npm run check`
Expected: build creates `dist/index.html`; `astro check` reports 0 errors.

- [ ] **Step 7: Commit**

```bash
git add src/web/site/src src/web/site/tests
git commit -m "feat(web): base layout with head, OpenGraph, and self-hosted fonts"
```

---

### Task 3: Shell — Button, Nav, Footer

**Files:**
- Create: `src/web/site/src/components/Button.astro`
- Create: `src/web/site/src/components/Nav.astro`
- Create: `src/web/site/src/components/Footer.astro`
- Modify: `src/web/site/src/layouts/Base.astro`
- Create: `src/web/site/tests/shell.test.ts`

**Interfaces:**
- Consumes: `Base.astro` (Task 2).
- Produces:
  - `Button.astro` — `Props { href: string; variant?: 'primary' | 'ghost' }`; renders `<a class="btn btn--{variant}" href={href}><slot /></a>`.
  - `Nav.astro` — no props; renders the `poddle` wordmark, Docs + Source links, and a primary CTA.
  - `Footer.astro` — no props; renders link columns, the "Open source" note, and the repo link.
  - `Base.astro` now renders `<Nav />` before `<main>` and `<Footer />` after it.

- [ ] **Step 1: Write the failing test**

`src/web/site/tests/shell.test.ts`:
```ts
import { experimental_AstroContainer as AstroContainer } from 'astro/container';
import { expect, test } from 'vitest';
import Button from '../src/components/Button.astro';
import Nav from '../src/components/Nav.astro';
import Footer from '../src/components/Footer.astro';

test('Button renders variant class, href, and label', async () => {
  const container = await AstroContainer.create();
  const html = await container.renderToString(Button, {
    props: { href: '/docs', variant: 'ghost' },
    slots: { default: 'Read the docs' },
  });
  expect(html).toContain('btn--ghost');
  expect(html).toContain('href="/docs"');
  expect(html).toContain('Read the docs');
});

test('Nav shows brand, Docs link, and CTA', async () => {
  const container = await AstroContainer.create();
  const html = await container.renderToString(Nav);
  expect(html).toContain('poddle');
  expect(html).toContain('Docs');
  expect(html).toContain('Self-host free');
});

test('Footer shows brand, repo link, and open-source note', async () => {
  const container = await AstroContainer.create();
  const html = await container.renderToString(Footer);
  expect(html).toContain('github.com/datadir-lab/poddle');
  expect(html).toContain('Open source');
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd src/web/site && npx vitest run tests/shell.test.ts`
Expected: FAIL — cannot resolve the component modules.

- [ ] **Step 3: Implement `Button.astro`**

`src/web/site/src/components/Button.astro`:
```astro
---
interface Props {
  href: string;
  variant?: 'primary' | 'ghost';
}
const { href, variant = 'primary' } = Astro.props;
---
<a href={href} class:list={['btn', `btn--${variant}`]}><slot /></a>
```

- [ ] **Step 4: Implement `Nav.astro`**

`src/web/site/src/components/Nav.astro`:
```astro
---
import Button from './Button.astro';
const repo = 'https://github.com/datadir-lab/poddle';
---
<header class="nav">
  <div class="container nav__inner">
    <a class="nav__brand" href="/">poddle</a>
    <nav class="nav__links" aria-label="Primary">
      <a href="/docs">Docs</a>
      <a href={repo}>Source</a>
      <Button href="/docs" variant="primary">Self-host free</Button>
    </nav>
  </div>
</header>
<style>
  .nav { border-bottom: 1px solid var(--border); }
  .nav__inner { display: flex; align-items: center; justify-content: space-between; padding-block: 1.1rem; }
  .nav__brand { font-family: var(--font-serif); font-size: 1.6rem; color: var(--ink); letter-spacing: -0.01em; }
  .nav__links { display: flex; align-items: center; gap: 1.5rem; }
  .nav__links > a { color: var(--muted); font-size: 0.95rem; }
  .nav__links > a:hover { color: var(--ink); }
</style>
```

- [ ] **Step 5: Implement `Footer.astro`**

`src/web/site/src/components/Footer.astro`:
```astro
---
const repo = 'https://github.com/datadir-lab/poddle';
---
<footer class="footer">
  <div class="container footer__inner">
    <div class="footer__brand">
      <span class="footer__mark">poddle</span>
      <p class="footer__tag">Secret-safe dev sandboxes for coding agents.</p>
    </div>
    <div class="footer__cols">
      <div>
        <p class="footer__h">Product</p>
        <a href="/docs">Docs</a>
        <a href="/#how-it-works">How it works</a>
      </div>
      <div>
        <p class="footer__h">Project</p>
        <a href={repo}>Source</a>
        <a href={`${repo}/issues`}>Issues</a>
      </div>
    </div>
  </div>
  <div class="container footer__base">
    <span>Open source · self-hosted.</span>
    <a href={repo}>github.com/datadir-lab/poddle</a>
  </div>
</footer>
<style>
  .footer { border-top: 1px solid var(--border); margin-top: var(--space-section); padding-block: 3rem 2rem; }
  .footer__inner { display: flex; flex-wrap: wrap; gap: 2.5rem; justify-content: space-between; }
  .footer__mark { font-family: var(--font-serif); font-size: 1.4rem; }
  .footer__tag { color: var(--muted); max-width: 32ch; margin: 0.4rem 0 0; }
  .footer__cols { display: flex; gap: 3rem; }
  .footer__h { font-size: var(--fs-eyebrow); text-transform: uppercase; letter-spacing: 0.12em; color: var(--faint); margin: 0 0 0.6rem; }
  .footer__cols a { display: block; color: var(--muted); font-size: 0.95rem; margin-bottom: 0.35rem; }
  .footer__cols a:hover { color: var(--ink); }
  .footer__base { display: flex; flex-wrap: wrap; gap: 0.5rem 1.5rem; justify-content: space-between; margin-top: 2.5rem; color: var(--faint); font-size: 0.85rem; }
</style>
```

- [ ] **Step 6: Wire `Nav` and `Footer` into `Base.astro`**

In `src/web/site/src/layouts/Base.astro`, add these imports to the frontmatter (after the existing `import '../styles/global.css';` line):
```astro
import Nav from '../components/Nav.astro';
import Footer from '../components/Footer.astro';
```
And replace the `<body>` block with:
```astro
  <body>
    <Nav />
    <main><slot /></main>
    <Footer />
  </body>
```

- [ ] **Step 7: Run the test to verify it passes**

Run: `cd src/web/site && npx vitest run tests/shell.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 8: Verify build + check**

Run: `cd src/web/site && npm run build && npm run check`
Expected: build succeeds; `astro check` reports 0 errors.

- [ ] **Step 9: Commit**

```bash
git add src/web/site/src src/web/site/tests
git commit -m "feat(web): site shell — Button, Nav, Footer wired into Base"
```

---

### Task 4: Hero section

**Files:**
- Create: `src/web/site/src/components/Hero.astro`
- Modify: `src/web/site/src/pages/index.astro`
- Create: `src/web/site/tests/hero.test.ts`

**Interfaces:**
- Consumes: `Base.astro` (Task 2), `Button.astro` (Task 3).
- Produces: `Hero.astro` (no props) — the landing hero with eyebrow, Instrument-Serif headline (with an italic accent word), subhead, and two CTAs. Placed as the first child of `Base` in `index.astro`.

- [ ] **Step 1: Write the failing test**

`src/web/site/tests/hero.test.ts`:
```ts
import { experimental_AstroContainer as AstroContainer } from 'astro/container';
import { expect, test } from 'vitest';
import Hero from '../src/components/Hero.astro';

test('Hero renders eyebrow, headline, subhead, and both CTAs', async () => {
  const container = await AstroContainer.create();
  const html = await container.renderToString(Hero);
  expect(html).toContain('Self-hostable · Secretless');
  expect(html).toContain('leak out of');
  expect(html).toContain('not one vendor secret inside it');
  expect(html).toContain('Self-host free');
  expect(html).toContain('Read the docs');
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd src/web/site && npx vitest run tests/hero.test.ts`
Expected: FAIL — cannot resolve `../src/components/Hero.astro`.

- [ ] **Step 3: Implement `Hero.astro`**

`src/web/site/src/components/Hero.astro`:
```astro
---
import Button from './Button.astro';
---
<section class="hero">
  <div class="container hero__inner">
    <p class="eyebrow">Self-hostable · Secretless</p>
    <h1 class="hero__title">A sandbox your coding agent <em>can’t</em> leak out of.</h1>
    <p class="hero__sub">
      Spin up an isolated, reproducible pod on your own infrastructure — your agent
      fully authed, and not one vendor secret inside it.
    </p>
    <p class="hero__wit">Every secret stays home — especially the ones you forgot you had.</p>
    <div class="hero__cta">
      <Button href="/docs" variant="primary">Self-host free</Button>
      <Button href="/docs" variant="ghost">Read the docs →</Button>
    </div>
  </div>
</section>
<style>
  .hero { padding-block: clamp(3.5rem, 8vw, 6.5rem) var(--space-section); }
  .hero__title { font-size: var(--fs-hero); max-width: 16ch; margin: 1rem 0 0; }
  .hero__title em { font-style: italic; color: var(--accent); }
  .hero__sub { font-size: var(--fs-lead); color: var(--muted); max-width: 52ch; margin: 1.4rem 0 0; }
  .hero__wit { font-family: var(--font-serif); font-style: italic; font-size: 1.35rem; color: var(--accent); margin: 1.1rem 0 0; }
  .hero__cta { display: flex; flex-wrap: wrap; gap: 0.85rem; margin-top: 2rem; }
</style>
```

- [ ] **Step 4: Place `Hero` in `index.astro`**

Replace `src/web/site/src/pages/index.astro` with:
```astro
---
import Base from '../layouts/Base.astro';
import Hero from '../components/Hero.astro';
---
<Base title="poddle — secret-safe sandboxes for coding agents">
  <Hero />
</Base>
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd src/web/site && npx vitest run tests/hero.test.ts`
Expected: PASS.

- [ ] **Step 6: Verify build + check**

Run: `cd src/web/site && npm run build && npm run check`
Expected: build succeeds; `astro check` reports 0 errors.

- [ ] **Step 7: Commit**

```bash
git add src/web/site/src src/web/site/tests
git commit -m "feat(web): landing hero section"
```

---

### Task 5: Content sections — problem/solution, features, how-it-works, closing CTA

**Files:**
- Create: `src/web/site/src/components/Section.astro`
- Create: `src/web/site/src/components/Feature.astro`
- Create: `src/web/site/src/components/Step.astro`
- Modify: `src/web/site/src/pages/index.astro`
- Create: `src/web/site/tests/sections.test.ts`

**Interfaces:**
- Consumes: `Base` (Task 2), `Button` (Task 3), `Hero` (Task 4).
- Produces:
  - `Section.astro` — `Props { id?: string; eyebrow?: string; title?: string }`; a spaced wrapper rendering an optional eyebrow + `<h2>` and a default `<slot />`.
  - `Feature.astro` — `Props { title: string }`; a titled card with a `<slot />` body.
  - `Step.astro` — `Props { n: number; title: string; code?: string }`; a numbered step with an optional monospace command line.

- [ ] **Step 1: Write the failing test**

`src/web/site/tests/sections.test.ts`:
```ts
import { experimental_AstroContainer as AstroContainer } from 'astro/container';
import { expect, test } from 'vitest';
import Feature from '../src/components/Feature.astro';
import Step from '../src/components/Step.astro';

test('Feature renders its title and body', async () => {
  const container = await AstroContainer.create();
  const html = await container.renderToString(Feature, {
    props: { title: 'Secretless by construction' },
    slots: { default: 'the real secret never enters the pod' },
  });
  expect(html).toContain('Secretless by construction');
  expect(html).toContain('the real secret never enters the pod');
});

test('Step renders number, title, and command', async () => {
  const container = await AstroContainer.create();
  const html = await container.renderToString(Step, {
    props: { n: 2, title: 'Spin up', code: 'poddle up --identity work' },
  });
  expect(html).toContain('Spin up');
  expect(html).toContain('poddle up --identity work');
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd src/web/site && npx vitest run tests/sections.test.ts`
Expected: FAIL — cannot resolve the component modules.

- [ ] **Step 3: Implement `Section.astro`**

`src/web/site/src/components/Section.astro`:
```astro
---
interface Props {
  id?: string;
  eyebrow?: string;
  title?: string;
}
const { id, eyebrow, title } = Astro.props;
---
<section class="section" id={id}>
  <div class="container">
    {eyebrow && <p class="eyebrow">{eyebrow}</p>}
    {title && <h2 class="section__title">{title}</h2>}
    <slot />
  </div>
</section>
<style>
  .section { padding-block: var(--space-section); border-top: 1px solid var(--border); }
  .section__title { font-size: var(--fs-h2); max-width: 22ch; margin: 0.6rem 0 1.5rem; }
</style>
```

- [ ] **Step 4: Implement `Feature.astro`**

`src/web/site/src/components/Feature.astro`:
```astro
---
interface Props {
  title: string;
}
const { title } = Astro.props;
---
<div class="feature">
  <h3 class="feature__title">{title}</h3>
  <p class="feature__body"><slot /></p>
</div>
<style>
  .feature { padding: 1.4rem 0; }
  .feature__title { font-family: var(--font-sans); font-weight: 600; font-size: var(--fs-h3); margin: 0 0 0.4rem; }
  .feature__body { color: var(--muted); margin: 0; }
</style>
```

- [ ] **Step 5: Implement `Step.astro`**

`src/web/site/src/components/Step.astro`:
```astro
---
interface Props {
  n: number;
  title: string;
  code?: string;
}
const { n, title, code } = Astro.props;
---
<li class="step">
  <span class="step__n">{n}</span>
  <div class="step__body">
    <h3 class="step__title">{title}</h3>
    {code && <code class="step__code">{code}</code>}
  </div>
</li>
<style>
  .step { display: flex; gap: 1rem; list-style: none; padding: 0.9rem 0; }
  .step__n { font-family: var(--font-serif); font-size: 1.5rem; color: var(--accent); line-height: 1; width: 1.5rem; }
  .step__title { font-family: var(--font-sans); font-weight: 600; font-size: var(--fs-h3); margin: 0 0 0.4rem; }
  .step__code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.9rem; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 0.3rem 0.6rem; color: var(--ink); }
</style>
```

- [ ] **Step 6: Assemble the sections in `index.astro`**

Replace `src/web/site/src/pages/index.astro` with:
```astro
---
import Base from '../layouts/Base.astro';
import Hero from '../components/Hero.astro';
import Section from '../components/Section.astro';
import Feature from '../components/Feature.astro';
import Step from '../components/Step.astro';
import Button from '../components/Button.astro';

const repo = 'https://github.com/datadir-lab/poddle';
---
<Base title="poddle — secret-safe sandboxes for coding agents">
  <Hero />

  <Section eyebrow="The problem" title="Coding agents want your keys. Don’t give them any.">
    <p style="color:var(--muted);max-width:60ch">
      poddle keeps the real credential in a local broker. The pod holds a short-lived,
      revocable handle; an injecting gateway swaps the handle for the real secret on the
      wire. The agent authenticates normally — and no vendor secret ever lands inside the pod.
    </p>
  </Section>

  <Section eyebrow="Why poddle" title="Built for people who run their own stack.">
    <div class="features">
      <Feature title="Isolated, reproducible pods">
        podman-backed sandboxes, local or over SSH to your own host.
      </Feature>
      <Feature title="Secretless by construction">
        a broker issues revocable handles; the real secret never enters the pod.
      </Feature>
      <Feature title="Your infra, your rules">
        self-host on your machine or homelab; bring your own compute, wired to Forgejo and Woodpecker.
      </Feature>
      <Feature title="Bring your own agent">
        a harness registry — claude-code today; codex, aider, pi, and local planned.
      </Feature>
    </div>
  </Section>

  <Section id="how-it-works" eyebrow="How it works" title="Three commands, no secrets leaked.">
    <ol class="steps">
      <Step n={1} title="Add an identity" code="poddle identity add work" />
      <Step n={2} title="Spin up" code="poddle up --identity work" />
      <Step n={3} title="Work — then tear down" code="poddle down" />
    </ol>
  </Section>

  <Section title="Give your agents a room of their own.">
    <div class="closing">
      <Button href="/docs" variant="primary">Self-host free</Button>
      <Button href={repo} variant="ghost">Star on Forgejo →</Button>
    </div>
  </Section>
</Base>

<style>
  .features { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 1.5rem 3rem; }
  .steps { margin: 0; padding: 0; }
  .closing { display: flex; flex-wrap: wrap; gap: 0.85rem; }
</style>
```

- [ ] **Step 7: Run the test to verify it passes**

Run: `cd src/web/site && npx vitest run tests/sections.test.ts`
Expected: PASS (2 tests).

- [ ] **Step 8: Verify the whole suite, build, and check**

Run: `cd src/web/site && npm test && npm run build && npm run check`
Expected: all Vitest tests pass; build creates `dist/index.html`; `astro check` reports 0 errors.

- [ ] **Step 9: Verify the built page contains the hero + features (build smoke)**

Run (from `src/web/site`): `grep -q "leak out of" dist/index.html && grep -q "Secretless by construction" dist/index.html && echo OK`
Expected: prints `OK`.

- [ ] **Step 10: Commit**

```bash
git add src/web/site/src src/web/site/tests
git commit -m "feat(web): landing content — problem, features, how-it-works, closing CTA"
```

---

### Task 6: Taskfile targets + Woodpecker web pipeline

**Files:**
- Modify: `Taskfile.yml` (repo root)
- Create: `woodpecker/web.yaml`

**Interfaces:**
- Consumes: the built site (Tasks 1–5).
- Produces: `task web-dev`, `task web-build`, `task web-check` targets; a change-scoped Woodpecker pipeline that builds + typechecks the site independently of the Go `ci` pipeline.

> Note: task names use `web-*` (not `web:*`) to avoid go-task's `:` namespace separator. The Go `ci`/`test`/`arch` targets are untouched — the web pipeline stays independent.

- [ ] **Step 1: Add web targets to `Taskfile.yml`**

Insert these tasks under `tasks:` (e.g. after the `run:` task):
```yaml
  web-dev:
    desc: Run the marketing site dev server
    dir: src/web/site
    cmds:
      - npm run dev

  web-build:
    desc: Build the static marketing site (src/web/site)
    dir: src/web/site
    cmds:
      - npm ci
      - npm run build

  web-check:
    desc: Typecheck the marketing site (astro check)
    dir: src/web/site
    cmds:
      - npm ci
      - npm run check
```

- [ ] **Step 2: Verify the build target works**

Run: `task web-build`
Expected: `npm ci` installs from the lockfile, `astro build` creates `src/web/site/dist/index.html`.

- [ ] **Step 3: Verify the check target works**

Run: `task web-check`
Expected: `astro check` reports 0 errors.

- [ ] **Step 4: Create the Woodpecker web pipeline**

`woodpecker/web.yaml`:
```yaml
# Web pipeline: typecheck + build the marketing site (src/web/site).
# Runs only when web files change, and is independent of the Go `ci` gate.
#
# NOTE: like the other files here, this relies on the project's Woodpecker
# "Pipeline Path" being set to `woodpecker/`.

when:
  - event: [push, pull_request, manual]
    path:
      include: ['src/web/**']

steps:
  web:
    image: node:22
    commands:
      - cd src/web/site
      - npm ci
      - npm run check
      - npm run build
      - test -f dist/index.html
      - grep -q "leak out of" dist/index.html
```

- [ ] **Step 5: Confirm the Go CI is unaffected**

Run: `task ci`
Expected: `vet`, `test`, `arch`, `build` all pass exactly as before (no web involvement).

- [ ] **Step 6: Commit**

```bash
git add Taskfile.yml woodpecker/web.yaml
git commit -m "chore(web): task targets + Woodpecker pipeline for the site"
```

---

## Self-Review

**Spec coverage** (spec §→ task):
- §2 scope (landing + shell + tokens + build/CI) → Tasks 1–6. Reserved `dashboard/` → Task 1 (`.gitkeep`); docs deferred (content dir reserved, not built).
- §4 architecture / directory structure → Task 1 (dirs, config, tokens), components across Tasks 2–5.
- §5 design system (fonts, tokens, scale, voice) → Task 1 (`tokens.css`), Task 2 (self-hosted fonts), global styles; light-theme-only respected (no dark styles).
- §6 landing sections (Nav, Hero, Problem→Solution, Features, How-it-works, Closing CTA, Footer) → Task 3 (Nav/Footer), Task 4 (Hero), Task 5 (remaining sections). Copy matches spec.
- §7 build/CI/deploy → Task 6 (Taskfile `web-*`, Woodpecker `web.yaml`); gitignore → Task 1; hosting deferred (noted).
- §8 testing (astro check + build smoke) → `npm run check` in every task; build smoke → Task 5 Step 9 + Woodpecker grep.
- §9 boundaries → Task 6 Step 5 confirms `task ci` unaffected; non-Go dirs inert.

**Placeholder scan:** No "TBD/TODO/handle edge cases" left; every code step contains full file contents; the only intentional placeholder is `site: 'https://poddle.dev'` (canonical URL), explicitly flagged as revisit-when-hosting-chosen, which does not block the build.

**Type consistency:** `Base` `Props{title,description?}`; `Button` `Props{href,variant?}` used consistently by Nav (Task 3), Hero (Task 4), and index (Task 5); `Section` `Props{id?,eyebrow?,title?}`, `Feature` `Props{title}`, `Step` `Props{n,title,code?}` match their usages in `index.astro`. Token variable names in `tokens.css` (Task 1) match every `var(--…)` reference in `global.css` and component styles.
