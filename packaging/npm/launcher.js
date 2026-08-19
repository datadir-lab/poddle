#!/usr/bin/env node
'use strict';

// Launcher for @poddle/cli. The real binary ships in a platform-specific
// optionalDependency (@poddle/cli-<platform>-<arch>); npm installs only the one
// matching the host. Resolve it and exec with the same args, stdio, and exit
// code. No network, no postinstall - the binary is already on disk.

const { spawnSync } = require('node:child_process');

const platform = process.platform; // 'linux' | 'darwin' | 'win32'
const arch = process.arch; // 'x64' | 'arm64'
const pkg = `@poddle/cli-${platform}-${arch}`;
const binName = platform === 'win32' ? 'poddle.exe' : 'poddle';

let binPath;
try {
  binPath = require.resolve(`${pkg}/bin/${binName}`);
} catch {
  console.error(
    `poddle: no prebuilt binary for ${platform}-${arch}.\n` +
      `Expected the optional dependency ${pkg} to be installed; if your package\n` +
      `manager skipped optional dependencies, reinstall without that flag, or\n` +
      `install another way: https://github.com/datadir-lab/poddle#install`
  );
  process.exit(1);
}

const result = spawnSync(binPath, process.argv.slice(2), { stdio: 'inherit' });
if (result.error) {
  console.error(`poddle: failed to run ${binPath}: ${result.error.message}`);
  process.exit(1);
}
process.exit(result.status === null ? 1 : result.status);
