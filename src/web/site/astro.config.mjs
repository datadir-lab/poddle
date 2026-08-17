import { defineConfig } from 'astro/config';
import { fileURLToPath } from 'node:url';
import sitemap from '@astrojs/sitemap';

// Allow importing shared tokens from ../shared during `astro dev`.
const webRoot = fileURLToPath(new URL('..', import.meta.url));

export default defineConfig({
  site: 'https://poddle.dev', // placeholder canonical; revisit when hosting is chosen
  integrations: [sitemap()],
  vite: {
    server: { fs: { allow: [webRoot] } },
  },
});
