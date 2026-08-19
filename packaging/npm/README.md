# npm packaging for the poddle CLI

`npm i -g @poddle/cli` installs the `poddle` binary on any platform, using the
same pattern as esbuild, Biome, and turbo: a thin main package plus one
platform-specific package per target.

```
@poddle/cli                     what users install; a JS launcher + optionalDependencies
├── @poddle/cli-linux-x64       os: linux,  cpu: x64    ships the poddle binary
├── @poddle/cli-linux-arm64     os: linux,  cpu: arm64
├── @poddle/cli-darwin-x64      os: darwin, cpu: x64
├── @poddle/cli-darwin-arm64    os: darwin, cpu: arm64
├── @poddle/cli-win32-x64       os: win32,  cpu: x64    ships poddle.exe
└── @poddle/cli-win32-arm64     os: win32,  cpu: arm64
```

npm reads each platform package's `os`/`cpu` and installs only the one matching
the host, so a user downloads ~6 MB, not all six. The main package's
`bin/poddle` is [`launcher.js`](launcher.js): it resolves the installed platform
binary and execs it. No postinstall, no network at install time.

## Files

- `launcher.js` - the `bin/poddle` shim (resolve platform binary, exec).
- `generate.mjs` - emits all seven packages from a directory of binaries. Single
  source of truth for the platform matrix and package metadata.
- `build.sh` - downloads a release's six archives, extracts the binaries, and
  runs `generate.mjs`. Used by CI and the bootstrap.
- The binaries and generated packages are never committed; they are built per
  release from the goreleaser artifacts.

## Publishing (automated)

`.github/workflows/publish-cli.yml` runs after the Release workflow **succeeds**
(goreleaser plus every install smoke test green) and publishes all seven via npm
Trusted Publishing (OIDC) - no `NPM_TOKEN`. It is gated on Release success on
purpose: npm publishes can't be undone, so a release that fails verification
never reaches npm. Publishing is version-gated, so re-runs are no-ops.

## One-time bootstrap

Trusted Publishing can't be configured for a package that doesn't exist yet, so
the first publish of each package is manual, then trusted publishing takes over.

1. Build and publish all seven with your own npm login (nothing is stored):

   ```sh
   npm login                                   # your account + 2FA
   VER=$(gh release view --repo datadir-lab/poddle --json tagName -q .tagName)
   sh packaging/npm/build.sh "$VER" /tmp/npm-out
   for d in /tmp/npm-out/cli-* /tmp/npm-out/cli; do (cd "$d" && npm publish --access public); done
   ```

2. On npmjs.com, add a trusted publisher to **each** of the seven packages
   (Settings -> Trusted Publisher -> GitHub Actions):

   | Field | Value |
   |---|---|
   | Organization or user | `datadir-lab` |
   | Repository | `poddle` |
   | Workflow filename | `publish-cli.yml` |
   | Environment | *(blank)* |

After that, every release publishes the CLI automatically over OIDC.
