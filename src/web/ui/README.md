# @poddle/ui

The poddle design system: shared design tokens and CSS primitives used across
every poddle UI — this repo's marketing site, docs, and dashboard, and the
commercial cloud console.

**Licensed MIT** (see [LICENSE](./LICENSE)), unlike the rest of this repository,
which is AGPL-3.0. datadir holds the copyright and dual-licenses this design
layer, so it can be shared with the proprietary cloud without extending copyleft.
It is the single source of truth for the brand.

## Use

Inside this repo, the site and dashboard consume it via a local `file:`
dependency (`"@poddle/ui": "file:../ui"`):

```css
@import "@poddle/ui/tokens.css";  /* colors, type, spacing — CSS custom props */
@import "@poddle/ui/base.css";    /* reset, typography, forms, buttons */
@import "@poddle/ui/views.css";   /* styles for the @poddle/ui/views components */
```

It is also published to npm as `@poddle/ui` for external consumers such as the
cloud console, which install it the same way.
