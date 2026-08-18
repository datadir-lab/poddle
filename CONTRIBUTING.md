# Contributing to poddle

Thanks for your interest. poddle's core is open source (AGPL-3.0) and we welcome
issues and patches.

> Development currently happens on our Forgejo
> (`github.com/datadir-lab/poddle`). A public GitHub mirror
> (`github.com/datadir-lab/poddle`) is planned; until then, open issues and pull
> requests on Forgejo.

## Licensing of contributions

By contributing, you agree your contribution is licensed to the project under the
**AGPL-3.0** (the same license as the core), and you certify the **Developer
Certificate of Origin** (below) by adding a `Signed-off-by` line to every commit:

```bash
git commit -s -m "your message"
```

which appends:

```
Signed-off-by: Your Name <you@example.com>
```

Use your real name and a reachable email.

> Note: before opening to external contributions at scale, datadir may introduce
> a lightweight **CLA**, so it can keep offering the core under commercial terms
> alongside AGPL. Until then, the DCO applies. See [LICENSING.md](./LICENSING.md).

## Developer Certificate of Origin 1.1

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.
1 Letterman Drive
Suite D4700
San Francisco, CA, 94129

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.


Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

## Commits and hooks

- **Conventional Commits**: `type(scope): summary` (e.g. `feat(broker): ...`,
  `fix(up): ...`, `docs: ...`). Enforced by commitlint.
- **Sign off** every commit (`git commit -s`) per the DCO above.

Install the hooks once - they run commitlint and gofmt:

```bash
npm install        # installs dev tooling and runs `lefthook install`
```

## Before you open a PR

- `task ci` passes (fmt + vet + test + arch + build) - see `Taskfile.yml`.
- New behavior has a test at the right tier: unit tests co-located in `src/`,
  architecture boundaries via `task arch`, end-to-end under `tests/e2e/`.
- Keep the secret-safety and boundary invariants intact - the architecture tests
  (`task arch`) guard several of them.
- Website house style: plain hyphens, no em/en dashes (enforced by `task
  web-test`).

## Where things live

See the [Development](./README.md#development) section of the README and
[docs/design/open-core.md](./docs/design/open-core.md) for the open-core split.
