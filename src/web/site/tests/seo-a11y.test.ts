import { experimental_AstroContainer as AstroContainer } from 'astro/container';
import { expect, test } from 'vitest';
import Base from '../src/layouts/Base.astro';
import DocsLayout from '../src/layouts/DocsLayout.astro';
import { GET as robotsGET } from '../src/pages/robots.txt';
import { GET as llmsGET } from '../src/pages/llms.txt';

const site = new URL('https://poddle.dev/');
const ctx = { site } as unknown as Parameters<typeof robotsGET>[0];

async function render(Component: Parameters<AstroContainer['renderToString']>[0], props: Record<string, unknown>) {
  const container = await AstroContainer.create();
  return container.renderToString(Component, { props });
}

// --- SEO / structured data (Base) ------------------------------------------

test('Base emits JSON-LD (Organization + WebSite + SoftwareApplication)', async () => {
  const html = await render(Base, { title: 'x' });
  expect(html).toContain('application/ld+json');
  expect(html).toContain('"Organization"');
  expect(html).toContain('"WebSite"');
  expect(html).toContain('"SoftwareApplication"');
});

test('Base emits canonical, OpenGraph and Twitter card meta', async () => {
  const html = await render(Base, { title: 'x' });
  expect(html).toContain('rel="canonical"');
  expect(html).toContain('property="og:image"');
  expect(html).toContain('name="twitter:card"');
});

// --- Landmark structure (DocsLayout) ---------------------------------------

test('DocsLayout has exactly one <main> (no nested/duplicate landmark)', async () => {
  const html = await render(DocsLayout, { title: 'Docs' });
  const mains = html.match(/<main[\s>]/g) ?? [];
  expect(mains.length).toBe(1);
});

test('DocsLayout defers the Pagefind stylesheet and mounts the search modal', async () => {
  const html = await render(DocsLayout, { title: 'Docs' });
  expect(html).toContain('media="print"'); // non-render-blocking pagefind CSS
  expect(html).toContain('id="docsearch-open"');
  expect(html).toContain('id="docsearch-modal"');
});

// --- Generated robots.txt / llms.txt ---------------------------------------

test('robots.txt allows all crawlers and points at sitemap + llms.txt', async () => {
  const body = await (await robotsGET(ctx)).text();
  expect(body).toContain('User-agent: *');
  expect(body).toContain('Allow: /');
  expect(body).toContain('https://poddle.dev/sitemap-index.xml');
  expect(body).toContain('https://poddle.dev/llms.txt');
});

test('llms.txt maps the docs using the configured base URL', async () => {
  const body = await (await llmsGET(ctx)).text();
  expect(body).toContain('# poddle');
  expect(body).toContain('https://poddle.dev/docs/security');
  expect(body).toContain('https://poddle.dev/docs/commands');
});
