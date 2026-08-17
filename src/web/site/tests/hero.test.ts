import { experimental_AstroContainer as AstroContainer } from 'astro/container';
import { expect, test } from 'vitest';
import Hero from '../src/components/Hero.astro';

test('Hero renders eyebrow, headline, subhead, and both CTAs', async () => {
  const container = await AstroContainer.create();
  const html = await container.renderToString(Hero);
  expect(html).toContain('Secretless · Isolated');
  expect(html).toContain('leak out of');
  expect(html).toContain('not one vendor secret inside it');
  expect(html).toContain('Get started');
  expect(html).toContain('Read the docs');
});
