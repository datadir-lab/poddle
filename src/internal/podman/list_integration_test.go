//go:build integration

package podman

import (
	"os/exec"
	"testing"

	pexec "github.com/datadir-lab/poddle/src/internal/exec"
)

// TestList_AgainstRealPodman verifies List parses a real `podman ps`. It creates
// a poddle-labeled container, lists via the provider, and asserts it appears.
// Skips when podman is not installed.
func TestList_AgainstRealPodman(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not installed; skipping integration test")
	}

	const name = "poddle-it-box"
	_ = exec.Command("podman", "rm", "-f", name).Run() // clean slate
	if out, err := exec.Command("podman", "run", "-d", "--name", name,
		"--label", "poddle.managed=true",
		"--label", "poddle.name="+name,
		"--label", "poddle.size=weak",
		"docker.io/library/alpine:latest", "sleep", "120",
	).CombinedOutput(); err != nil {
		t.Fatalf("podman run: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", name).Run() })

	list, err := New(pexec.OS{}, "").List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, s := range list {
		if s.Name == name {
			found = true
			if s.State != "running" {
				t.Errorf("state = %q, want running", s.State)
			}
		}
	}
	if !found {
		t.Fatalf("container %q not found in list: %+v", name, list)
	}
}
