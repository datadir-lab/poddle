# Open-core architecture

How poddle separates the open-source core from the commercial cloud and
enterprise editions, and why the split is drawn where it is.

See [LICENSING.md](../../LICENSING.md) for the license terms; this document is the
*engineering* contract that keeps those terms enforceable.

## Tiers

| Tier | Contents | License | Repository |
|---|---|---|---|
| **Core** | CLI (`src/cli`); broker + kernel (`src/internal`: injection, redaction, egress policy, tamper-evident audit); website (`src/web`) | AGPL-3.0 | this repo (public) |
| **Enterprise** | SSO / SCIM & SAML, audit export & retention, DORA / AI-Act controls | proprietary | `poddle-cloud` (private) |
| **Cloud** | Multi-tenant control plane, provisioning, billing, managed-pod orchestration | proprietary | `poddle-cloud` (private) |

The core is a complete, self-hostable product on its own. Enterprise and cloud
are additive; they never fork or replace the core.

## Repository topology

```
github.com/datadir-lab/poddle          public,  AGPL-3.0     (this repo)
github.com/datadir-lab/poddle-cloud     private, proprietary
```

- **poddle** (this repo) is the whole open-source product. When we publish to
  GitHub (`github.com/datadir-lab/poddle`) it is a straight mirror of this repo -
  there is nothing to redact, because the closed code lives in a different repo.
- **poddle-cloud** is private and never mirrored. It depends on the core the way
  any operator would: it runs the released `poddle` broker.

Keeping closed code in a **separate repo** (not a filtered subdirectory) makes the
boundary a repository boundary - impossible to leak through a mirror
misconfiguration, and clean history on both sides.

## The arms-length rule (the important one)

The core is AGPL-3.0, and AGPL copyleft reaches any program that **links** it: a
Go binary that imports the core's packages is a derivative work and must itself be
AGPL. To keep the proprietary editions proprietary, `poddle-cloud`:

- **MUST NOT** import core Go packages
  (`github.com/datadir-lab/poddle/src/internal/...` or `.../src/cli/...`).
- **DOES** operate the core as a separate program: it runs the released `poddle` /
  `poddled` binary and talks to it over the CLI and control API.

Separate programs communicating over a process/API boundary are separate works;
copyleft does not cross that line. If a future feature genuinely needs to embed
the core in-process, that requires a CLA-backed dual-license (see below), not an
exception to this rule.

datadir, as copyright holder, may separately dual-license its own core code into
the proprietary editions; the arms-length rule is the belt-and-suspenders that
keeps the model clean regardless, and is required the moment any third-party AGPL
contribution lands in the core.

## Contributions and the CLA trigger

Today datadir is the sole author, so it owns all core copyright and can
dual-license freely; contributions use the [DCO](../../CONTRIBUTING.md). **Before
accepting external contributions**, adopt a CLA so datadir retains the right to
offer the core commercially - otherwise third-party AGPL code cannot be embedded
in, or dual-licensed into, the proprietary editions.

## Enforcement

- `poddle-cloud` carries a boundary test that fails the build if it imports a core
  `internal`/`cli` package - mirroring this repo's own `task arch` boundary tests.
- The cloud consumes core capabilities only through the broker's released CLI /
  control API, versioned like any external contract.
- This repo stays self-contained: nothing here imports or references the private
  repo, so the public mirror is always complete and buildable on its own.
