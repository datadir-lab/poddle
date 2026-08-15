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

// TestE2E_Local_UpAndLs starts a poddle directly on the runner's local podman,
// then lists it. This is the "starts directly on the runner" e2e.
func TestE2E_Local_UpAndLs(t *testing.T) {
	requirePodman(t)

	bin := buildBinary(t)
	const name = "poddle-e2e-local"
	_ = exec.Command("podman", "rm", "-f", name).Run()
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", name).Run() })

	if out, err := exec.Command(bin, "up", name, "--detach",
		"--image", "docker.io/library/alpine:latest").CombinedOutput(); err != nil {
		t.Fatalf("poddle up: %v\n%s", err, out)
	}

	out, err := exec.Command(bin, "ls").CombinedOutput()
	if err != nil {
		t.Fatalf("poddle ls: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), name) {
		t.Fatalf("ls did not list %q:\n%s", name, out)
	}
}
