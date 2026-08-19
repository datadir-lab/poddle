# Testing poddle

Four tiers, fast to slow. Fast tiers run on every push (container-free);
container-backed tiers need an engine, in a dedicated pipeline or locally.

| Tier | Location | Needs | Run |
|---|---|---|---|
| **Unit** | `src/**/*_test.go` | nothing (fakes) | `task test` |
| **Architecture** | `tests/architecture/` | nothing (`go list`) | `task arch` |
| **Integration** | `src/**/*_integration_test.go` (white-box) · `tests/integration/` (black-box) | a container engine | `task integration` |
| **E2E** | `tests/e2e/` | the built binary (+ engine for real flows) | `task e2e` |

Integration and e2e carry build tags (`//go:build integration`, `//go:build
e2e`), so `go test ./...` stays fast and container-free; both **skip gracefully**
with no engine present.

## Why some tests live in `src/` and some in `tests/`

Go's `internal/` rule: `src/internal/*` is importable only from within `src/`. A
test needing **white-box** access to a kernel package (e.g. driving the podman
provider directly) must be **co-located** there. Tests under `tests/` are
**black-box**: they drive the built binary or public surface only.

- provider ↔ real podman → co-located (`src/internal/podman/*_integration_test.go`)
- binary ↔ simulated remote host → `tests/integration/` (black-box, testcontainers)

## The Engine architecture makes this tractable

Commands depend on `engine.Engine`, not podman:
- **Unit**: inject a fake Runner/Engine; microsecond tests, no containers.
- **Integration**: run the *real* in-process engine (podman) against a real engine.
- **Remote**: point a target at a container that *plays the role of a remote host* (below).

## Simulating a remote host with testcontainers

poddle's remote path is `CLI → ssh → remote host (podman / poddled)`. To test it
in CI without a real server, spin a container that **is** a remote host (sshd +
podman) with `testcontainers-go`, point a target at `ssh://tester@localhost:<port>`,
and run the flow; the connection-aware provider (`podman.New(runner, conn)`) runs
the same code path.

### The remote-host image - `tests/integration/remotehost/Containerfile`

```dockerfile
FROM quay.io/podman/stable
RUN dnf install -y openssh-server && ssh-keygen -A
RUN useradd -m tester && mkdir -p /home/tester/.ssh
COPY test_key.pub /home/tester/.ssh/authorized_keys
RUN chown -R tester:tester /home/tester/.ssh \
 && chmod 600 /home/tester/.ssh/authorized_keys
RUN loginctl enable-linger tester || true    # keep the rootless podman socket alive
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D"]
```

### The harness - `tests/integration/remote_test.go`

```go
//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startRemoteHost boots the sshd+podman image and returns an ssh conn URL that
// poddle's connection-aware provider can target.
func startRemoteHost(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{Context: "remotehost", Dockerfile: "Containerfile"},
			ExposedPorts:   []string{"22/tcp"},
			Privileged:     true, // podman-in-container needs it
			WaitingFor:     wait.ForListeningPort("22/tcp").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start remote host: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "22/tcp")
	return "ssh://tester@" + host + ":" + port.Port() + "/run/user/1000/podman/podman.sock"
}

func TestRemote_ListsSandboxes(t *testing.T) {
	conn := startRemoteHost(t)
	// Drive the built `poddle` binary with a target whose conn == this, run
	// `poddle up` on the remote, then `poddle ls` and assert the sandbox shows.
	_ = conn
}
```

**Gotchas (honest):**
- **Nested containers**: podman-in-container needs `--privileged` (or rootless + `/dev/fuse`, cgroups v2); the CI runner must allow it.
- **Rootless socket**: the flow targets `/run/user/<uid>/podman/podman.sock`; enable it + linger in the image.
- **Slow**: image build + boot. Keep these in a separate pipeline, not the per-push gate.
- **SSH key**: generate a throwaway keypair for the fixture; never a real key.

> Status: the harness above is the documented target. It lands with the remote
> Engine (`poddled` / remote-podman) on a container-capable host where it can be
> run and verified. Until then, `tests/integration/` holds the black-box
> scaffolding and the co-located white-box provider test.

## CI mapping (GitHub Actions)

- `.github/workflows/ci.yml`: `task ci` (vet + fmt + unit + arch + build), the web build/typecheck, govulncheck, and golangci-lint. Every push and PR, fast, container-free.
- `.github/workflows/e2e.yml`: the `task e2e-*` suites on podman. Manual ("Run workflow"), all suites or a subset. 17 of 19 pass on GitHub's rootless podman; `e2e-move` and `e2e-task` need rootful podman, so run them via the `suites` input on a rootful/self-hosted runner. The retired Woodpecker pipelines are frozen on branch `archive/woodpecker-ci`.
- `.github/workflows/codeql.yml`, `scorecard.yml`, `release.yml`: code scanning, supply-chain scoring, and signed releases.

## Run locally

```
task test          # unit - always
task arch          # boundaries - always
task integration   # needs podman/docker; skips if absent
task e2e           # builds + drives the binary
task test-all      # everything
```
