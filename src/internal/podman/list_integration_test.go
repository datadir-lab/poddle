//go:build integration

package podman

import (
	"os/exec"
	"testing"

	pexec "git.dev.datadir.co/datadir/poddle/src/internal/exec"
	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
)

// TestProvider_Lifecycle_AgainstRealPodman exercises the full provider lifecycle
// against a real podman: Create -> List (present) -> Remove -> List (absent).
// Skips when podman is not installed.
func TestProvider_Lifecycle_AgainstRealPodman(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not installed; skipping integration test")
	}

	p := New(pexec.OS{}, "")
	const name = "poddle-it-box"
	_ = exec.Command("podman", "rm", "-f", name).Run() // clean slate
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", name).Run() })

	id, err := p.Create(sandbox.Spec{
		Name: name, Image: "docker.io/library/alpine:latest",
		Template: "base", Runtime: "container", Size: "weak",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !listed(t, p, name) {
		t.Fatalf("sandbox %q not listed after create", name)
	}

	if err := p.Remove(id); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if listed(t, p, name) {
		t.Fatalf("sandbox %q still listed after remove", name)
	}
}

func listed(t *testing.T, p *Provider, name string) bool {
	t.Helper()
	list, err := p.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, s := range list {
		if s.Name == name {
			return true
		}
	}
	return false
}
