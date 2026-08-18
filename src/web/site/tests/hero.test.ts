import { experimental_AstroContainer as AstroContainer } from 'astro/container';
import { expect, test } from 'vitest';
import Hero from '../src/components/Hero.astro';

test('Hero renders headline, subhead, and both CTAs', async () => {
  const container = await AstroContainer.create();
  const html = await container.renderToString(Hero);
  expect(html).toContain('leak out of');
  expect(html).toContain('not one vendor secret inside it');
  expect(html).toContain('Start free');
  expect(html).toContain('Self-host it');
});
