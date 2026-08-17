import { experimental_AstroContainer as AstroContainer } from 'astro/container';
import { expect, test } from 'vitest';
import Base from '../src/layouts/Base.astro';

test('Base renders html lang, title, and slotted content', async () => {
  const container = await AstroContainer.create();
  const html = await container.renderToString(Base, {
    props: { title: 'poddle - secret-safe sandboxes' },
    slots: { default: '<p>SLOT_MARKER</p>' },
  });
  expect(html).toContain('<html lang="en"');
  expect(html).toContain('<title>poddle - secret-safe sandboxes</title>');
  expect(html).toContain('SLOT_MARKER');
});
