# Changelog

poddle follows [Semantic Versioning](https://semver.org). Full, per-commit release
notes are generated from [Conventional Commits](https://www.conventionalcommits.org)
and published on the
[GitHub Releases](https://github.com/datadir-lab/poddle/releases) page; this file
is the human summary. Security-relevant fixes are called out under a `Security`
heading with their advisory IDs, per [SECURITY.md](./SECURITY.md).

## [Unreleased]

## [0.1.2] - 2026-08-20

### Added

- The `poddle dashboard` console matured into a full local audit UI: a left
  sidebar with overview charts, a live audit stream with richer filters and CSV
  export, keyboard navigation, loading and responsive states, a policy-insight
  view with a live dry-run, a destinations view, and per-pod controls to rebind a
  policy or revoke a pod's credentials. Audit-chain provenance is surfaced with a
  one-click verify affordance.
- A shared `@poddle/ui` design system consumed by both the marketing site and the
  dashboard.
- `poddled` honours a `PODDLE_SOCKET` override to isolate its control socket
  (parallel daemons and tests).
- `GOVERNANCE.md`, `docs/security-design.md`, and an OpenSSF Best Practices
  self-assessment (`docs/openssf-best-practices.md`); the project is registered
  and passing on bestpractices.dev.
- A redesigned README with a Fraunces wordmark lockup, a dashboard hero, and an
  animated terminal demo.

### Security

- `install.sh` verifies the release's cosign signature (keyless, against the
  release workflow's identity) before installing, and fails closed unless
  `PODDLE_SKIP_VERIFY=1`. The release workflow smoke-tests this install path.
- Release artifacts carry SLSA build provenance (`poddle.intoto.jsonl`) alongside
  the cosign signature over the checksums, and the release build is reproducible
  (trimmed paths, zeroed build id, pinned module timestamp).
- Enabled `gosec`, `gocritic`, and `revive` in CI, and set `ReadHeaderTimeout` on
  every HTTP server (broker, dashboard, the `poddled` control socket, and the
  egress forward proxy) to close the no-timeout / Slowloris class.
- Upgraded the website/dashboard build toolchain to clear known advisories in
  build-time dev dependencies: `vite` 5 -> 8 and `esbuild` (dashboard;
  GHSA-67mh-4wv8-2f99 and related), and `astro` 5 -> 7, `sharp` -> 0.35, and
  `esbuild` (marketing site; incl. GHSA-f88m-g3jw-g9cj, the astro XSS series, and
  GHSA-g7r4-m6w7-qqqr). These are build/dev tools for the static site and console
  SPA; they are not present in the released `poddle` binary. `osv-scanner` is
  clean across all lockfiles.
- Hardened the supply chain for a higher OpenSSF Scorecard: CodeQL now covers
  JavaScript/TypeScript as well as Go, dependency-review and gitleaks run in CI,
  and CI tool installs are pinned via corepack.

## [0.1.1] - 2026-08-19

### Added

- Distribution: a Homebrew cask, a Scoop bucket, Linux `.deb` / `.rpm` / `.apk`
  packages, and a `curl | sh` installer.

### Changed

- Migrated CI from Woodpecker to GitHub Actions.

### Security

- Hardened the L4 wire parsers against panics and DoS: bounded RESP array and
  bulk-string lengths and hardened upstream auth parsing, with fuzz targets over
  the hand-rolled parsers and the policy engine.
- Rejected path traversal in policy names and updated vulnerable indirect
  dependencies.

## [0.1.0] - 2026-08-18

First tagged release. The secretless broker and isolated, disposable pods;
identities and revocable handles; connectors (git hosts, CI, registries,
databases, LLM); governance (per-request egress policy + tamper-evident audit)
and the `poddle dashboard` console; the marketing site; and the open-core
(AGPL-3.0) licensing.

[Unreleased]: https://github.com/datadir-lab/poddle/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/datadir-lab/poddle/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/datadir-lab/poddle/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/datadir-lab/poddle/releases/tag/v0.1.0
