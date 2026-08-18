import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from 'vitest';

// House style: normal hyphens only in rendered content - no em or en dashes.
// This lint scans the web source and the generated cli.json so a fancy dash
// can't slip into a page (from an .astro file or a Go command description).
const ROOT = fileURLToPath(new URL('..', import.meta.url));
const FANCY_DASH = /[—–]/; // em (U+2014), en (U+2013)

function collect(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === 'dist' || name === '.astro') continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) collect(p, out);
    else if (/\.(astro|css)$/.test(name) || (name.endsWith('.ts') && !name.endsWith('.test.ts'))) out.push(p);
  }
  return out;
}

test('no em/en dashes in rendered content - use a normal hyphen', () => {
  const files = [...collect(join(ROOT, 'src')), join(ROOT, 'src/data/cli.json')];
  const offenders: string[] = [];
  for (const file of files) {
    readFileSync(file, 'utf8').split('\n').forEach((line, i) => {
      if (FANCY_DASH.test(line)) offenders.push(`${relative(ROOT, file)}:${i + 1}`);
    });
  }
  expect(offenders, `Replace em/en dashes with a hyphen:\n  ${offenders.join('\n  ')}`).toEqual([]);
});
