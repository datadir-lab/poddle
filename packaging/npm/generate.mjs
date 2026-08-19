#!/usr/bin/env node
// Generate the @poddle/cli npm packages from the released Go binaries.
//
//   node generate.mjs --version 0.1.1 --staging <dir> --out <dir>
//
// <staging> holds the extracted binaries, one per platform, at
//   <staging>/<goos>_<goarch>/poddle       (poddle.exe on windows)
// <out> receives one directory per package, each ready to `npm publish`:
//   <out>/cli                     -> @poddle/cli (the launcher + optionalDeps)
//   <out>/cli-<os>-<cpu>          -> @poddle/cli-<os>-<cpu> (one binary)
//
// This file is the single source of truth for the platform matrix and the
// package metadata; the publish workflow just runs it and publishes <out>/*.

import { readFileSync, writeFileSync, mkdirSync, copyFileSync, chmodSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, '..', '..');

// goos/goarch (release naming) -> npm os/cpu (process.platform/process.arch).
const PLATFORMS = [
  { goos: 'linux', goarch: 'amd64', os: 'linux', cpu: 'x64' },
  { goos: 'linux', goarch: 'arm64', os: 'linux', cpu: 'arm64' },
  { goos: 'darwin', goarch: 'amd64', os: 'darwin', cpu: 'x64' },
  { goos: 'darwin', goarch: 'arm64', os: 'darwin', cpu: 'arm64' },
  { goos: 'windows', goarch: 'amd64', os: 'win32', cpu: 'x64' },
  { goos: 'windows', goarch: 'arm64', os: 'win32', cpu: 'arm64' },
];

const REPOSITORY = {
  type: 'git',
  url: 'git+https://github.com/datadir-lab/poddle.git',
  directory: 'packaging/npm',
};
const LICENSE = 'AGPL-3.0-only';

const { values } = parseArgs({
  options: {
    version: { type: 'string' },
    staging: { type: 'string' },
    out: { type: 'string' },
  },
});
const version = values.version?.replace(/^v/, '');
const staging = values.staging;
const out = values.out;
if (!version || !staging || !out) {
  console.error('usage: generate.mjs --version <v> --staging <dir> --out <dir>');
  process.exit(2);
}

const write = (dir, name, contents) => {
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, name), contents);
};
const writeJSON = (dir, obj) => write(dir, 'package.json', JSON.stringify(obj, null, 2) + '\n');
const licenseText = readFileSync(join(repoRoot, 'LICENSE'), 'utf8');

// --- platform packages ---------------------------------------------------
const optionalDependencies = {};
for (const p of PLATFORMS) {
  const pkgName = `@poddle/cli-${p.os}-${p.cpu}`;
  optionalDependencies[pkgName] = version;

  const dir = join(out, `cli-${p.os}-${p.cpu}`);
  const binName = p.os === 'win32' ? 'poddle.exe' : 'poddle';
  const src = join(staging, `${p.goos}_${p.goarch}`, binName);
  const binDir = join(dir, 'bin');
  mkdirSync(binDir, { recursive: true });
  copyFileSync(src, join(binDir, binName));
  if (p.os !== 'win32') chmodSync(join(binDir, binName), 0o755);

  writeJSON(dir, {
    name: pkgName,
    version,
    description: `poddle CLI binary for ${p.os}-${p.cpu}.`,
    license: LICENSE,
    repository: REPOSITORY,
    os: [p.os],
    cpu: [p.cpu],
    files: ['bin'],
    publishConfig: { access: 'public' },
  });
  write(dir, 'LICENSE', licenseText);
  write(
    dir,
    'README.md',
    `# ${pkgName}\n\nPrebuilt poddle CLI binary for ${p.os}/${p.cpu}. Do not install ` +
      `this directly - install [\`@poddle/cli\`](https://www.npmjs.com/package/@poddle/cli), ` +
      `which pulls in the right binary for your platform.\n`
  );
}

// --- main package --------------------------------------------------------
const cliDir = join(out, 'cli');
const binDir = join(cliDir, 'bin');
mkdirSync(binDir, { recursive: true });
copyFileSync(join(here, 'launcher.js'), join(binDir, 'poddle'));
chmodSync(join(binDir, 'poddle'), 0o755);

writeJSON(cliDir, {
  name: '@poddle/cli',
  version,
  description:
    'poddle: self-hostable, secret-safe dev sandboxes for coding agents. Installs the poddle CLI.',
  license: LICENSE,
  repository: REPOSITORY,
  homepage: 'https://github.com/datadir-lab/poddle',
  bin: { poddle: 'bin/poddle' },
  files: ['bin'],
  optionalDependencies,
  engines: { node: '>=16' },
  publishConfig: { access: 'public' },
});
write(cliDir, 'LICENSE', licenseText);
write(
  cliDir,
  'README.md',
  `# @poddle/cli\n\n` +
    `Self-hostable, secret-safe dev sandboxes for coding agents.\n\n` +
    '```sh\nnpm i -g @poddle/cli\npoddle up\n```\n\n' +
    `The right prebuilt binary for your platform is installed as an optional ` +
    `dependency (\`@poddle/cli-<platform>-<arch>\`); there is no postinstall ` +
    `download. Needs [podman](https://podman.io) to run pods.\n\n` +
    `Other install methods and full docs: ` +
    `https://github.com/datadir-lab/poddle#install\n`
);

console.log(`Generated @poddle/cli@${version} + ${PLATFORMS.length} platform packages in ${out}`);
