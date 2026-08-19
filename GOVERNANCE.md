# Governance

poddle's core (this repository) is open source under the
[AGPL-3.0](./LICENSE) and stewarded by **datadir s. r. o.**, which also offers
the commercial poddle cloud and enterprise editions (see
[LICENSING.md](./LICENSING.md)). This document describes how the project is run:
who decides what, how changes get in, and how to get involved.

## Roles

- **Contributors** — anyone who opens an issue or pull request. No prior status
  is required. Contributions are accepted under the DCO (see below).
- **Maintainers** — trusted reviewers with commit access who review and merge
  pull requests, triage issues, and cut releases. The current maintainers are
  employed or sponsored by datadir; the project is young and maintainer-led.
- **Steward** — datadir s. r. o. holds the copyright, owns the trademarks and
  the `datadir-lab` organization, and has the final say on project direction and
  the open-core boundary.

As the project and its contributor base grow, we intend to open maintainership
to sustained external contributors. Until then, this document describes the
lightweight model we actually run today rather than one we aspire to.

## Decision-making

- **Ordinary changes** (bug fixes, features, docs) are decided by **maintainer
  review** on a pull request. One maintainer approval that is not the author is
  the goal for every non-trivial change; see [CONTRIBUTING.md](./CONTRIBUTING.md).
- **Significant changes** (new architectural boundaries, security-sensitive
  behavior, anything touching the core/cloud split) are discussed in an issue or
  a design doc under [`docs/design/`](./docs/design) before implementation.
- **Disagreements** are resolved by discussion aiming for consensus among
  maintainers. Where consensus is not reached, the steward decides.

## Becoming a maintainer

We invite contributors to maintainership after a sustained track record of
high-quality reviews and contributions, and demonstrated understanding of
poddle's secret-safety and open-core invariants. There is no fixed quota; a
current maintainer nominates, and the steward confirms.

## Contribution process

- All changes land through **pull requests** on
  [GitHub](https://github.com/datadir-lab/poddle); CI must pass and the change
  should carry a test at the appropriate tier (see
  [TESTING.md](./TESTING.md)).
- Every commit is **signed off** under the Developer Certificate of Origin
  (`git commit -s`), per [CONTRIBUTING.md](./CONTRIBUTING.md). A lightweight CLA
  may be introduced before large-scale external contribution; until then the DCO
  governs.
- Commits follow **Conventional Commits**; commitlint and the git hooks enforce
  format on the way in.

## Releases

Releases are cut from `main` by a maintainer tagging a semver version. The
release is built, signed (cosign), and published by CI
([`.github/workflows/release.yml`](./.github/workflows/release.yml)). Release
notes are published on the
[GitHub Releases](https://github.com/datadir-lab/poddle/releases) page and
summarized in [CHANGELOG.md](./CHANGELOG.md); security-relevant fixes are called
out explicitly (see [SECURITY.md](./SECURITY.md)).

## Code of conduct

Participation is governed by our
[Code of Conduct](./CODE_OF_CONDUCT.md). Report concerns to the contacts listed
there.

## Security

Please do **not** file security issues in public. Follow the private reporting
process in [SECURITY.md](./SECURITY.md). Our secure-design posture and
cryptography inventory are documented in
[docs/security-design.md](./docs/security-design.md).

## Changing this document

Governance changes are themselves pull requests to this file, decided by
maintainer consensus with steward confirmation.
