//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestE2E_Remote_Loopback drives the full remote code path — poddle talking to
// podman over SSH — reliably, without a fragile nested-host sim: the CI fixture
// runs sshd + a podman API socket on the runner, sets PODDLE_HOST=ssh://
// root@127.0.0.1/…, and poddle drives the runner's own podman over ssh. The
// pods are local; the transport is remote, which is the part that has code.
func TestE2E_Remote_Loopback(t *testing.T) {
	if os.Getenv("PODDLE_HOST") == "" {
		t.Skip("needs a loopback-ssh PODDLE_HOST (set up by the e2e-remote workflow)")
	}
	bin := buildBinary(t)
	env := os.Environ() // PODDLE_HOST + SSH_AUTH_SOCK come from the fixture

	poddle := func(args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Env = env
		out, err := c.CombinedOutput()
		return string(out), err
	}

	pod := "poddle-remote-loop"
	_, _ = poddle("down", pod) // best-effort pre-clean
	t.Cleanup(func() { _, _ = poddle("down", pod) })

	if out, err := poddle("up", pod, "--detach", "--image", "docker.io/library/alpine:latest"); err != nil {
		t.Fatalf("remote up over ssh failed: %v\n%s", err, out)
	}
	ls, err := poddle("ls")
	if err != nil || !strings.Contains(ls, pod) {
		t.Fatalf("remote ls should list %q: %v\n%s", pod, err, ls)
	}
	if out, err := poddle("down", pod); err != nil {
		t.Fatalf("remote down over ssh failed: %v\n%s", err, out)
	}
	if ls2, _ := poddle("ls"); strings.Contains(ls2, pod) {
		t.Errorf("remote ls should not list %q after down:\n%s", pod, ls2)
	}
}
