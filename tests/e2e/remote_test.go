//go:build e2e

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func ptr(s string) *string { return &s }

// TestE2E_Remote_Lifecycle boots a container that plays a remote host (sshd +
// podman), points poddle at it via PODDLE_HOST=ssh://..., and runs the full
// up -> ls -> down -> ls loop remotely. Needs the podman CLI (poddle's ssh
// client), ssh-keygen, and a docker-compatible engine for testcontainers.
//
// NOTE: reliably bringing up the rootless podman API socket for `tester` inside
// the container is the fixture detail finalized on a real CI runner.
func TestE2E_Remote_Lifecycle(t *testing.T) {
	requirePodman(t)
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available; skipping remote e2e")
	}

	// throwaway keypair for the fixture (never a real key)
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(sshDir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    "remotehost",
				Dockerfile: "Containerfile",
				BuildArgs:  map[string]*string{"PUBKEY": ptr(strings.TrimSpace(string(pub)))},
				KeepImage:  true,
			},
			ExposedPorts: []string{"22/tcp"},
			Privileged:   true, // podman-in-container
			WaitingFor:   wait.ForListeningPort("22/tcp").WithStartupTimeout(120 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("cannot start remote-host container (needs a docker-compatible engine): %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := c.MappedPort(ctx, "22/tcp")
	if err != nil {
		t.Fatal(err)
	}

	// ssh config so podman-over-ssh finds the key and skips host-key prompts
	sshCfg := "Host *\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  IdentityFile " + key + "\n"
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(sshCfg), 0o600); err != nil {
		t.Fatal(err)
	}

	conn := "ssh://tester@" + host + ":" + port.Port() + "/run/user/1000/podman/podman.sock"
	env := append(os.Environ(), "HOME="+home, "PODDLE_HOST="+conn)

	bin := buildBinary(t)
	const name = "poddle-e2e-remote"

	poddle(t, bin, env, "up", name, "--detach", "--image", "docker.io/library/alpine:latest")

	if ls := poddle(t, bin, env, "ls"); !strings.Contains(ls, name) {
		t.Fatalf("remote ls should list %q after up:\n%s", name, ls)
	}

	poddle(t, bin, env, "down", name)

	if ls := poddle(t, bin, env, "ls"); strings.Contains(ls, name) {
		t.Fatalf("remote ls should NOT list %q after down:\n%s", name, ls)
	}
}
