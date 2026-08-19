# Licensing

poddle is **open core**. The core is free and open source; the hosted cloud and
enterprise editions are commercial. This file explains what is licensed how.

> Summary, not legal advice. The [LICENSE](./LICENSE) file and each edition's
> agreement are the authoritative terms. datadir s. r. o. is the copyright holder.

## What's in this repository

Everything in this repository is the **poddle core**, licensed under the **GNU
Affero General Public License v3.0** (AGPL-3.0) - see [LICENSE](./LICENSE).

You can:

- run poddle for any purpose, including inside a company;
- read, modify, and self-host it, including over a network;
- redistribute it under the same AGPL-3.0 terms.

AGPL's one condition that matters here (section 13): if you run a **modified**
poddle as a network service, you must offer your users the corresponding source
of your modified version. Running it unmodified, or self-hosting for your own
use, carries no such obligation.

The core includes the CLI (`src/cli`), the broker and kernel (`src/internal` -
credential injection, egress redaction, egress policy, and the tamper-evident
audit log), and this website (`src/web`).

## What is NOT in this repository

Two things are commercial and live in a **separate, private** repository
(`poddle-cloud`), under proprietary terms, not AGPL:

| Edition | What it is | Where |
|---|---|---|
| **poddle cloud** | The hosted control plane: multi-tenancy, provisioning, billing, orchestration of managed pods. | `poddle-cloud` (private) |
| **poddle enterprise** | SSO / SCIM & SAML, audit-log export & retention, DORA / AI-Act compliance controls, dedicated support. | `poddle-cloud` (private) |

These are the paid product. They are not open source and are not distributed
here.

## How a proprietary edition coexists with an AGPL core

This is the part to get right, so it is spelled out:

1. **datadir owns the core copyright.** A copyright holder is not bound by the
   AGPL it grants to others - datadir may also license *its own* core code into
   *its own* proprietary editions under separate commercial terms
   (dual-licensing). This is the mechanism Grafana, MongoDB, and GitLab use.

2. **The boundary is kept arms-length.** The proprietary control plane operates
   the AGPL broker as a **separate service** - it runs the released `poddle` /
   `poddled` binary and talks to it over the CLI and control API, rather than
   importing the AGPL Go packages into a proprietary binary. Separate programs
   communicating over a process/API boundary are separate works, so AGPL
   copyleft does not reach across that line. See
   [docs/design/open-core.md](./docs/design/open-core.md).

3. **Contributions (today: DCO).** Contributions are accepted under the
   [Developer Certificate of Origin](./CONTRIBUTING.md) and licensed to the
   project under AGPL-3.0. While datadir is the sole author this preserves the
   dual-licensing right automatically. **Before accepting external
   contributions, datadir will adopt a CLA** so it keeps the right to offer the
   core under commercial terms; without one, third-party AGPL contributions
   could not be embedded in or dual-licensed into a proprietary edition.

## Need a different license?

If AGPL-3.0 doesn't work for you - for example, you want to embed poddle in a
proprietary product without AGPL obligations - a **commercial license** is
available. Email [hello@datadir.co](mailto:hello@datadir.co).
