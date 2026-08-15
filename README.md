# poddle

Self-hostable, secret-safe dev sandboxes for coding agents.

`poddle up` provisions an isolated, reproducible sandbox on your own infra —
wired to your self-hosted stack (Forgejo / Woodpecker), with your coding agent
authed via your subscription and your secrets kept out by construction.

> **Status:** scaffolding only — no implementation yet.

## Layout

```
src/cli/              CLI entry + one vertical slice per command (up, ls, ...)
src/internal/         shared kernel (private): runner, podman provider, config
tests/architecture/   structural rules enforcing slice boundaries
tests/e2e/            end-to-end tests driving the built binary
woodpecker/           CI pipelines
Taskfile.yml          all dev commands
```

## Commands (via [Task](https://taskfile.dev))

```
task build   # build the binary
task test    # unit tests (co-located in src/)
task arch    # architecture / boundary tests
task e2e     # end-to-end tests
task ci      # what CI runs (vet + test + arch + build)
```

## Module

`github.com/datadir-lab/poddle` — imports carry the `src/` segment, e.g.
`github.com/datadir-lab/poddle/src/internal/podman`.
