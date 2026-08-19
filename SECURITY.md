# Security Policy

## Reporting a vulnerability

**Email [security@datadir.co](mailto:security@datadir.co).** Don't open a public
issue, pull request, or discussion for security reports.

We acknowledge good-faith reports within **two business days**, credit reporters
who want it, and won't pursue legal action against good-faith research. Please
give us a reasonable window to ship a fix before public disclosure.

Full policy: <https://poddle.dev/security> and
<https://poddle.dev/.well-known/security.txt>.

## Scope

Covers the poddle core here (the CLI and broker). The hosted poddle cloud is
covered separately at the same contact.

## Supported versions

poddle is pre-1.0 and moves fast; security fixes land on `main`. Supported
versions will be listed here once tagged releases begin.

## Disclosure in release notes

Every release that fixes a publicly known, security-relevant vulnerability —
whether in poddle itself or in a bundled dependency — names it in the release
notes and in [CHANGELOG.md](./CHANGELOG.md) under a `Security` heading, with the
advisory identifier (CVE / GHSA) where one exists. Routine dependency updates
with no known vulnerability are not called out this way.

## How we find vulnerabilities

Dependencies are watched continuously: Dependabot opens update PRs,
[osv-scanner](./osv-scanner.toml) and `govulncheck` run in CI, CodeQL provides
static analysis, and the [OpenSSF Scorecard](https://securityscorecards.dev/viewer/?uri=github.com/datadir-lab/poddle)
tracks the project's security posture. See
[docs/security-design.md](./docs/security-design.md) for the secure-design and
cryptography posture.
