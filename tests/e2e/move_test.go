//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2E_Move_KeepsStateResizesShell proves stateless pods + `poddle move`: a
// file written to /workspace survives a move onto a fresh, re-sized shell (a new
// container), and `down` purges the session volumes.
func TestE2E_Move_KeepsStateResizesShell(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/debian:stable-slim\"\nsize = \"weak\"\n")

	env := append(os.Environ(), "XDG_RUNTIME_DIR="+filepath.Join(proj, "run"))
	pod := "poddle-move-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_, _ = exec.Command("sh", "-c", "podman volume ls -q --filter label=poddle.pod="+pod+" | xargs -r podman volume rm").CombinedOutput()
	})

	inspectID := func() string {
		out, _ := exec.Command("podman", "inspect", "-f", "{{.Id}}", pod).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	run := func(args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir, c.Env = proj, env
		out, err := c.CombinedOutput()
		return string(out), err
	}

	if out, err := run("up", pod, "--detach"); err != nil {
		t.Fatalf("up: %v\n%s", err, out)
	}
	if out, err := run("run", pod, "echo hello-state > /workspace/marker.txt"); err != nil {
		t.Fatalf("write marker: %v\n%s", err, out)
	}
	id1 := inspectID()

	if out, err := run("move", pod, "--size", "strong", "--detach"); err != nil {
		t.Fatalf("move: %v\n%s", err, out)
	}
	id2 := inspectID()
	if id1 == "" || id1 == id2 {
		t.Fatalf("move should create a new container (id1=%q id2=%q)", id1, id2)
	}

	// The workspace state survived the move.
	out, err := run("run", pod, "cat /workspace/marker.txt")
	if err != nil || !strings.Contains(out, "hello-state") {
		t.Fatalf("workspace state lost on move: %v\n%s", err, out)
	}
	// The new shell is re-sized (create-time --cpus works even rootless).
	insp, _ := exec.Command("podman", "inspect", "-f", "{{.HostConfig.NanoCpus}}", pod).CombinedOutput()
	if !strings.Contains(string(insp), "8000000000") {
		t.Errorf("moved shell not resized to strong: %q", insp)
	}

	// down purges the session volumes.
	if out, err := run("down", pod); err != nil {
		t.Fatalf("down: %v\n%s", err, out)
	}
	vols, _ := exec.Command("podman", "volume", "ls", "-q").CombinedOutput()
	if strings.Contains(string(vols), "poddle-"+pod+"-workspace") {
		t.Errorf("session volumes should be purged on down:\n%s", vols)
	}
}
