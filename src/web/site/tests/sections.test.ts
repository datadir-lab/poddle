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
