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
  expect(html).toContain('Get started');
});

test('Footer shows brand, repo link, and open-source note', async () => {
  const container = await AstroContainer.create();
  const html = await container.renderToString(Footer);
  expect(html).toContain('git.dev.datadir.co/datadir/poddle');
  expect(html).toContain('Source');
});
