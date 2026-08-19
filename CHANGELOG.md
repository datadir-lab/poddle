# Changelog

poddle follows [Semantic Versioning](https://semver.org). Full, per-commit release
notes are generated from [Conventional Commits](https://www.conventionalcommits.org)
and published on the
[GitHub Releases](https://github.com/datadir-lab/poddle/releases) page; this file
is the human summary. Security-relevant fixes are called out under a `Security`
heading with their advisory IDs, per [SECURITY.md](./SECURITY.md).

## [Unreleased]

### Security

- Upgraded the website/dashboard build toolchain to clear known advisories in
  build-time dev dependencies: `vite` 5 -> 8 and `esbuild` (dashboard;
  GHSA-67mh-4wv8-2f99 and related), and `astro` 5 -> 7, `sharp` -> 0.35, and
  `esbuild` (marketing site; incl. GHSA-f88m-g3jw-g9cj, the astro XSS series,
  and GHSA-g7r4-m6w7-qqqr). These are build/dev tools for the static site and
  console SPA; they are not present in the released `poddle` binary. `osv-scanner`
  is now clean across all lockfiles.
- Release artifacts now carry SLSA build provenance (`poddle.intoto.jsonl`)
  alongside the existing cosign signature over the checksums.

### Added

- `GOVERNANCE.md`, `docs/security-design.md`, and an OpenSSF Best Practices
  self-assessment (`docs/openssf-best-practices.md`).

## [0.1.0] - 2026-08-18

First tagged release. The secretless broker and isolated, disposable pods;
identities and revocable handles; connectors (git hosts, CI, registries,
databases, LLM); governance (per-request egress policy + tamper-evident audit)
and the `poddle dashboard` console; the marketing site; and the open-core
(AGPL-3.0) licensing.

[Unreleased]: https://github.com/datadir-lab/poddle/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/datadir-lab/poddle/releases/tag/v0.1.0
