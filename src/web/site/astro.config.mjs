import { defineConfig } from 'astro/config';
import { fileURLToPath } from 'node:url';
import sitemap from '@astrojs/sitemap';
import pagefind from 'astro-pagefind';

// Allow importing shared tokens from ../shared during `astro dev`.
const webRoot = fileURLToPath(new URL('..', import.meta.url));

export default defineConfig({
  site: 'https://poddle.dev', // placeholder canonical; revisit when hosting is chosen
  // pagefind() indexes the built site AND serves /pagefind/* during `astro dev`
  // (from the last build), so the docs search works in dev too.
  integrations: [sitemap(), pagefind()],
  vite: {
    server: { fs: { allow: [webRoot] } },
  },
});
