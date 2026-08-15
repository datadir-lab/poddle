//go:build e2e

package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

// requirePodman skips a test when the podman CLI is unavailable.
func requirePodman(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not installed; skipping")
	}
}

// poddle runs the built binary (optionally with env) and fails the test on a
// non-zero exit, returning combined output.
func poddle(t *testing.T, bin string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("poddle %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestE2E_Local_Lifecycle runs the full up -> ls -> down -> ls loop against the
// runner's local podman, asserting the poddle appears, then is gone after down.
func TestE2E_Local_Lifecycle(t *testing.T) {
	requirePodman(t)

	bin := buildBinary(t)
	const name = "poddle-e2e-local"
	_ = exec.Command("podman", "rm", "-f", name).Run() // clean slate
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", name).Run() })

	poddle(t, bin, nil, "up", name, "--detach", "--image", "docker.io/library/alpine:latest")

	if ls := poddle(t, bin, nil, "ls"); !strings.Contains(ls, name) {
		t.Fatalf("ls should list %q after up:\n%s", name, ls)
	}

	poddle(t, bin, nil, "down", name)

	if ls := poddle(t, bin, nil, "ls"); strings.Contains(ls, name) {
		t.Fatalf("ls should NOT list %q after down:\n%s", name, ls)
	}
}
