//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// requirePodman skips a test when the podman CLI is unavailable, and — since
// every brokered pod now launches the containerized broker — builds the local
// broker image and points PODDLE_BROKER_IMAGE at it so `up`'s EnsureRunning
// doesn't try to pull the unpublished ghcr image.
func requirePodman(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not installed; skipping")
	}
	ensureBrokerImage(t)
}

var brokerImageOnce sync.Once

// ensureBrokerImage builds poddle-broker:test from Containerfile.broker (once per
// test binary) and points PODDLE_BROKER_IMAGE at it, so `up`'s EnsureRunning
// launches the local broker container instead of pulling the unpublished ghcr
// image. Tests set cmd.Env = append(os.Environ(), ...), so this Setenv
// propagates into the `up` subprocess.
func ensureBrokerImage(t *testing.T) {
	t.Helper()
	brokerImageOnce.Do(func() {
		root := repoRoot(t)
		out, err := exec.Command("podman", "build",
			"-f", filepath.Join(root, "Containerfile.broker"),
			"-t", "poddle-broker:test", root).CombinedOutput()
		if err != nil {
			t.Fatalf("build broker image: %v\n%s", err, out)
		}
		os.Setenv("PODDLE_BROKER_IMAGE", "poddle-broker:test")
	})
}

// repoRoot walks up from the test's working directory until it finds the repo
// root — the directory holding both go.mod and Containerfile.broker.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	start := dir
	for {
		_, goMod := os.Stat(filepath.Join(dir, "go.mod"))
		_, broker := os.Stat(filepath.Join(dir, "Containerfile.broker"))
		if goMod == nil && broker == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root (go.mod + Containerfile.broker) not found walking up from %s", start)
		}
		dir = parent
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
