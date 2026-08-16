# tests/e2e — end-to-end tests

Build-tagged (`//go:build e2e`, run via `task e2e`). Builds the `poddle` binary
and drives it as a black box. Later slices exercise real sandboxes and require
Podman available on the runner.

## You do NOT need podman locally to develop

The test layers and where they run:

| Layer | Command | Needs | Run it |
|-------|---------|-------|--------|
| unit + architecture | `task ci` | nothing (fakes) | anywhere — the inner loop |
| secretless round-trip | `task e2e-claude` | **docker** | locally (Docker Desktop) |
| podman lifecycle / remote | `task e2e` | podman, or docker→podman via testcontainers | CI (Woodpecker) |

`podman.go` is unit-tested with `exec.Fake` (it asserts the exact `podman`
argument lists), so you develop podman logic against fakes — no podman needed.
Only "does podman actually run" needs a real engine, and that's what `task e2e`
in CI is for.

**Recommended loop:** write code + `task ci` locally → `task e2e-claude` on
Docker when you touch the broker/harness/secretless path → push → Woodpecker
runs the podman e2e. No local podman required.

### `task e2e-claude` (secretless, real Claude Code)

`TestE2E_Secretless_RealClaudeCode` (in `src/internal/broker`, `//go:build
e2e`) runs the real Claude Code CLI in a `node:22` container against a real
broker (sentinel secret + mock Anthropic upstream) and asserts the upstream saw
the real secret and never the handle. It uses `docker` and, by default, reaches
the broker via `host.docker.internal`. For CI (broker in the step container,
claude as a sibling), override:

- `PODDLE_E2E_BROKER_HOST` — how the claude container addresses the broker (CI: `127.0.0.1`).
- `PODDLE_E2E_DOCKER_NETWORK` — extra `--network` for the claude container (CI: `container:<step>` to share the step's netns).

### `task e2e-up` (full `poddle up`, provider-parameterized)

`TestE2E_Up_Secretless` drives the real `poddle up --identity --exec` binary
against **podman** for each provider case in the `upCases` table (`anthropic`
today). Select which run with **`PODDLE_E2E_PROVIDERS`** (comma list; empty =
all). Adding a provider/harness = one row in `upCases` + its own mock upstream.
`woodpecker/e2e-up.yaml` runs it on every push (nested podman).
