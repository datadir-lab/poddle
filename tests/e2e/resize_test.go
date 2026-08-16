//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2E_Up_Resize proves `poddle resize`: a running pod's CPU/memory limits
// are live-updated (no restart), confirmed via podman inspect.
func TestE2E_Up_Resize(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/debian:stable-slim\"\nsize = \"weak\"\n")

	pod := "poddle-resize-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", pod).Run() })

	up := exec.Command(bin, "up", pod, "--detach")
	up.Dir, up.Env = proj, os.Environ()
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	// Live-resize the CPU ceiling to 8 cores (no restart). Memory-cap update is
	// not permitted under rootless podman, so we resize CPU only here.
	rz := exec.Command(bin, "resize", pod, "--cpus", "8")
	if out, err := rz.CombinedOutput(); err != nil {
		t.Fatalf("resize failed: %v\n%s", err, out)
	}

	insp, err := exec.Command("podman", "inspect", "-f", "{{.HostConfig.NanoCpus}}", pod).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect: %v\n%s", err, insp)
	}
	if got := strings.TrimSpace(string(insp)); got != "8000000000" { // 8 cpus in NanoCpus
		t.Errorf("cpus not live-resized to 8, NanoCpus = %q", got)
	}
}
