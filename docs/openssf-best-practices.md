# OpenSSF Best Practices & Scorecard — self-assessment

This is poddle's working self-assessment against two OpenSSF programs:

1. **[OpenSSF Best Practices Badge](https://www.bestpractices.dev)** — a
   self-certified questionnaire (passing / silver / gold). The tables below map
   every *passing*-level criterion to the evidence in this repository and give
   the answer to enter on bestpractices.dev.
2. **[OpenSSF Scorecard](https://securityscorecards.dev/viewer/?uri=github.com/datadir-lab/poddle)**
   — an automated score out of 10. The Scorecard section records each check's
   state and what moves it.

Status legend: ✅ met · 🔧 met by a change in this branch · ⚠️ needs a maintainer
action (see the [checklist](#maintainer-action-checklist)) · N/A not applicable.

> To claim the badge: register the project at
> <https://www.bestpractices.dev/en/projects/new> (log in with the GitHub
> account that owns `datadir-lab/poddle`), then work down the tables below —
> almost every answer is "Met" with the linked evidence. Paste the project ID
> into the README badge (see the checklist).

## Basics

| Criterion | Level | Status | Evidence / answer |
|---|---|---|---|
| description_good | MUST | ✅ | `README.md` and <https://poddle.dev> describe what poddle does. |
| interact | MUST | ✅ | README (install/usage), `CONTRIBUTING.md`, GitHub Issues. |
| contribution | MUST | ✅ | `CONTRIBUTING.md` documents the PR-based process. |
| contribution_requirements | SHOULD | ✅ | `CONTRIBUTING.md`: Conventional Commits, DCO sign-off, tests, `task ci`. |
| floss_license | MUST | ✅ | AGPL-3.0 (`LICENSE`). |
| floss_license_osi | SUGGESTED | ✅ | AGPL-3.0 is OSI-approved. |
| license_location | MUST | ✅ | `LICENSE` at repository root. |
| documentation_basics | MUST | ✅ | `README.md`, `docs/`, and the docs site at poddle.dev/docs. |
| documentation_interface | MUST | ✅ | CLI documented in README, `poddle --help`, and generated `docs/commands` (`task web-docs`). |
| sites_https | MUST | ✅ | GitHub and poddle.dev are HTTPS-only. |
| discussion | MUST | ✅ | GitHub Issues are searchable and URL-addressable. Enabling Discussions (checklist) makes this stronger. |
| english | SHOULD | ✅ | Documentation and issues are in English. |
| maintained | MUST | ✅ | Actively maintained (see commit history). |

## Change Control

| Criterion | Level | Status | Evidence / answer |
|---|---|---|---|
| repo_public | MUST | ✅ | Public repo at `github.com/datadir-lab/poddle`. |
| repo_track | MUST | ✅ | Git tracks changes, authors, and dates. |
| repo_interim | MUST | ✅ | Interim commits are public between releases. |
| repo_distributed | SUGGESTED | ✅ | Git (distributed VCS). |
| version_unique | MUST | ✅ | Unique semver tags per release. |
| version_semver | SUGGESTED | ✅ | Semantic Versioning (`CHANGELOG.md`). |
| version_tags | SUGGESTED | ✅ | Releases tagged (`v0.1.0`, `v0.1.1`, …). |
| release_notes | MUST | ✅ | `CHANGELOG.md` + GitHub Releases (human-readable). |
| release_notes_vulns | MUST | 🔧 | Policy documented in `SECURITY.md` ("Disclosure in release notes") and `CHANGELOG.md`: security fixes named under a `Security` heading with CVE/GHSA IDs. |

## Reporting

| Criterion | Level | Status | Evidence / answer |
|---|---|---|---|
| report_process | MUST | ✅ | GitHub Issues; `CONTRIBUTING.md`. |
| report_tracker | SHOULD | ✅ | GitHub Issues. |
| report_responses | MUST | ⚠️ | Maintainer attestation: we respond to a majority of bug reports. Young project — keep responding as issues arrive. |
| enhancement_responses | SHOULD | ⚠️ | Maintainer attestation of responsiveness to enhancement requests. |
| report_archive | MUST | ✅ | GitHub Issues provide a public, searchable archive. |
| vulnerability_report_process | MUST | ✅ | `SECURITY.md` publishes the process. |
| vulnerability_report_private | MUST | ✅ | Private report channel: <security@datadir.co>. |
| vulnerability_report_response | MUST | ✅ | `SECURITY.md`: acknowledgement within two business days (≤ 14 days). |

## Quality

| Criterion | Level | Status | Evidence / answer |
|---|---|---|---|
| build | MUST | ✅ | `task build` (go build); `Taskfile.yml`. |
| build_common_tools | SUGGESTED | ✅ | Go toolchain + Task. |
| build_floss_tools | SHOULD | ✅ | Builds with only FLOSS tools. |
| test | MUST | ✅ | `go test` unit + architecture + e2e suites (FLOSS). |
| test_invocation | SHOULD | ✅ | `task test` / `task ci`. |
| test_most | SUGGESTED | ✅ | Four-tier tests + coverage (Codecov badge). |
| test_continuous_integration | SUGGESTED | ✅ | GitHub Actions `ci.yml`, `e2e.yml`. |
| test_policy | MUST | ✅ | `CONTRIBUTING.md` / `TESTING.md`: new behavior requires a test. |
| tests_are_added | MUST | ✅ | History shows tests land with features; parser fuzz targets exist. |
| tests_documented_added | SUGGESTED | ✅ | Documented in `CONTRIBUTING.md` and `TESTING.md`. |
| warnings | MUST | ✅ | golangci-lint + `go vet` in CI (`.golangci.yml`). |
| warnings_fixed | MUST | ✅ | CI is blocking; lint/vet failures fail the build. |
| warnings_strict | SUGGESTED | ✅ | golangci-lint config; `astro check` strict for the site. |

## Security

| Criterion | Level | Status | Evidence / answer |
|---|---|---|---|
| know_secure_design | MUST | ✅ | `docs/security-design.md` (threat model, secretless design). Maintainer attests. |
| know_common_errors | MUST | ✅ | `docs/security-design.md` + fuzzing + CodeQL. Maintainer attests. |
| crypto_published | MUST | ✅ | SCRAM-SHA-256 (RFC 5802/7677), TLS — see `docs/security-design.md`. |
| crypto_call | SHOULD | ✅ | Go stdlib + `golang.org/x/crypto`; no home-rolled primitives. |
| crypto_floss | MUST | ✅ | All crypto is FLOSS. |
| crypto_keylength | MUST | ✅ | SCRAM-SHA-256 32-byte key; TLS follows Go defaults (meet NIST minimums). |
| crypto_working | MUST | ✅ | No broken algorithms used for security. |
| crypto_weaknesses | SHOULD | ✅ | No SHA-1/MD5-based security constructions. |
| crypto_pfs | SHOULD | ✅ | TLS uses ECDHE (perfect forward secrecy) by Go default. |
| crypto_password_storage | MUST | N/A | poddle authenticates no end-users and stores no user passwords; service credentials live in memory (memguard). See `docs/security-design.md`. |
| crypto_random | MUST | ✅ | `crypto/rand` for handles and nonces. |
| delivery_mitm | MUST | ✅ | Downloads over HTTPS; releases cosign-signed + checksummed; git over HTTPS. |
| delivery_unsigned | MUST | ✅ | `install.sh` fetches over HTTPS and verifies the checksum; no hashes retrieved over HTTP. |
| vulnerabilities_fixed_60_days | MUST | 🔧 | osv-scanner now clean across all lockfiles; Dependabot + `govulncheck` + osv-scanner run continuously. |
| vulnerabilities_critical_fixed | SHOULD | ✅ | Dependabot/osv process; this branch cleared 14 dependency advisories. |
| no_leaked_credentials | MUST | ✅ | No credentials committed. Enable secret scanning + push protection to keep it that way (checklist). |

## Analysis

| Criterion | Level | Status | Evidence / answer |
|---|---|---|---|
| static_analysis | MUST | ✅ | CodeQL (`codeql.yml`) + golangci-lint. |
| static_analysis_common_vulnerabilities | SUGGESTED | ✅ | CodeQL default + security queries. |
| static_analysis_fixed | MUST | ✅ | No outstanding medium+ findings. |
| static_analysis_often | SUGGESTED | ✅ | CodeQL on push/PR and on a schedule. |
| dynamic_analysis | SUGGESTED | ✅ | Go native fuzzing of the L4 parsers, SCRAM, and policy engine. |
| dynamic_analysis_unsafe | SUGGESTED | N/A | Go is memory-safe; parsers are still fuzzed. |
| dynamic_analysis_enable_assertions | SUGGESTED | ✅ | Tests run with the Go race detector (`task test`). |
| dynamic_analysis_fixed | MUST | ✅ | No outstanding fuzz/dynamic findings. |

## OpenSSF Scorecard

Score at the time of writing: **7.1**. The zeros are the levers; the rest is at
or near maximum.

| Check | Score | Action |
|---|---|---|
| Vulnerabilities | 0 → **10** | 🔧 Fixed: bumped `vite`, `esbuild`, `astro`, `sharp` in `src/web/*`; osv-scanner is clean. |
| Signed-Releases | 8 → **10** | 🔧 Fixed: SLSA provenance (`poddle.intoto.jsonl`) added to `release.yml`, verifiable on the next tagged release. |
| Branch-Protection | −1 (error) | 🔧 Wired `SCORECARD_TOKEN` (admin-read PAT) into `scorecard.yml`; the secret is set. Tighten protection (checklist) so the revealed score is high. |
| CII-Best-Practices | 0 | ⚠️ Register + earn the Best Practices badge (this document); Scorecard reads the live badge. |
| Maintained | 0 | ⚠️ Age-based ("created within 90 days"). Clears automatically ~90 days after repo creation with ongoing activity. No code change. |
| Code-Review | 0 | ⚠️ Route changes through PRs with a second-party approval (checklist). Improves as reviewed PRs accumulate. |
| Pinned-Dependencies | 8–10 | ✅ `main` pins actions by SHA. The one intentional exception is the SLSA reusable workflow, which must be tag-pinned. |
| Contributors | 6 | ⚠️ Organic — needs contributors from 3+ organizations. |
| Dependency-Update-Tool, Security-Policy, Dangerous-Workflow, Binary-Artifacts, Token-Permissions, License, SAST, CI-Tests, Fuzzing, Packaging | 10 | ✅ Keep as-is. |

## Maintainer action checklist

Items that only a repository admin can do (they are outward-facing or account
settings, not code):

- [ ] **Register the badge.** Create the project at
      <https://www.bestpractices.dev/en/projects/new>, fill the questionnaire
      using the tables above, and reach *passing*.
- [ ] **Add the badge to the README.** Replace `<PROJECT_ID>` and uncomment the
      badge line already placed in `README.md`.
- [ ] **Self-attest** `know_secure_design`, `know_common_errors`,
      `report_responses`, and `enhancement_responses` on bestpractices.dev
      (backed by `docs/security-design.md` and your issue responsiveness).
- [ ] **Enable GitHub Discussions** (strengthens `discussion`):
      `gh api -X PATCH repos/datadir-lab/poddle -f has_discussions=true`
- [ ] **Enable secret scanning + push protection** (supports
      `no_leaked_credentials`):
      `gh api -X PATCH repos/datadir-lab/poddle -f 'security_and_analysis[secret_scanning][status]=enabled' -f 'security_and_analysis[secret_scanning_push_protection][status]=enabled'`
- [ ] **Enable Dependabot security updates:**
      `gh api -X PUT repos/datadir-lab/poddle/automated-security-fixes`
- [ ] **Tighten branch protection** so the now-visible Branch-Protection score is
      high and to move Code-Review: require a pull-request review (≥1 approval)
      and, optionally, signed commits. Note the solo-maintainer tension — a
      second approver is needed for approvals to count.
- [ ] **Grow reviewers/contributors:** have a second maintainer approve PRs
      (moves Code-Review and Contributors).

`SCORECARD_TOKEN` (admin-read PAT) is already set as a repository secret, so the
Branch-Protection check will resolve on the next Scorecard run.
