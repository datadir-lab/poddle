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
	// The nested-container remote sim (testcontainers + podman-in-podman + ssh)
	// is fragile and unrepresentative; the real remote path is best validated
	// against an actual host. Opt in with PODDLE_E2E_REMOTE=1.
	if os.Getenv("PODDLE_E2E_REMOTE") == "" {
		t.Skip("nested remote e2e is opt-in; set PODDLE_E2E_REMOTE=1")
	}
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
				// don't keep the built image around after the run
			},
			ExposedPorts: []string{"22/tcp"},
			Privileged:   true, // podman-in-container
			// Reach the sibling by its direct container IP (below), so skip the
			// external host-port dial (the host-published port isn't reachable
			// from a sibling step on this runner).
			WaitingFor: wait.ForListeningPort("22/tcp").SkipExternalCheck().WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("cannot start remote-host container (needs a docker-compatible engine): %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	ip, err := c.ContainerIP(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// ssh config so podman-over-ssh finds the key and skips host-key prompts
	sshCfg := "Host *\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  IdentityFile " + key + "\n"
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(sshCfg), 0o600); err != nil {
		t.Fatal(err)
	}

	// reach the sibling directly on the shared docker bridge — the host-published
	// port isn't reachable from a sibling step on this runner.
	conn := "ssh://root@" + ip + ":22/run/podman/podman.sock"
	// podman's ssh client authenticates via ssh-agent; start one with the key.
	authSock, stopAgent := startSSHAgent(t, key)
	t.Cleanup(stopAgent)
	env := append(os.Environ(), "HOME="+home, "PODDLE_HOST="+conn, "SSH_AUTH_SOCK="+authSock)

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

// startSSHAgent boots an ssh-agent, adds the key, and returns its socket path
// plus a stop function.
func startSSHAgent(t *testing.T, key string) (sock string, stop func()) {
	t.Helper()
	out, err := exec.Command("ssh-agent", "-s").Output()
	if err != nil {
		t.Fatalf("ssh-agent: %v", err)
	}
	var pid string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "SSH_AUTH_SOCK="):
			sock = strings.SplitN(strings.TrimPrefix(line, "SSH_AUTH_SOCK="), ";", 2)[0]
		case strings.HasPrefix(line, "SSH_AGENT_PID="):
			pid = strings.SplitN(strings.TrimPrefix(line, "SSH_AGENT_PID="), ";", 2)[0]
		}
	}
	if sock == "" {
		t.Fatal("could not parse SSH_AUTH_SOCK from ssh-agent")
	}
	add := exec.Command("ssh-add", key)
	add.Env = append(os.Environ(), "SSH_AUTH_SOCK="+sock)
	if o, err := add.CombinedOutput(); err != nil {
		t.Fatalf("ssh-add: %v\n%s", err, o)
	}
	return sock, func() {
		if pid != "" {
			k := exec.Command("ssh-agent", "-k")
			k.Env = append(os.Environ(), "SSH_AGENT_PID="+pid)
			_ = k.Run()
		}
	}
}
