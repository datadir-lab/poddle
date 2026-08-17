//go:build e2e

package e2e

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2E_Poddled_DaemonStatus: `poddle daemon status` reports the running
// daemon and its active pods, and reflects `down` revoking a pod's handles.
func TestE2E_Poddled_DaemonStatus(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(mock.Close)

	xdg := t.TempDir()
	connDir := filepath.Join(xdg, "poddle", "connections", "svc")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"woodpecker\"\nbase_url = \""+mock.URL+"\"\nowner = \"local\"\n")
	writeFile(t, filepath.Join(connDir, "woodpecker-token"), "SENTINEL")

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/debian:stable-slim\"\nconnectors = [\"svc\"]\n")

	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		"XDG_RUNTIME_DIR="+filepath.Join(xdg, "run"))

	pod := "poddle-status-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("pkill", "-f", "daemon --socket").Run()
	})

	up := exec.Command(bin, "up", pod, "--detach")
	up.Dir, up.Env = proj, env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	status := func() string {
		s := exec.Command(bin, "daemon", "status")
		s.Env = env
		out, _ := s.CombinedOutput()
		return string(out)
	}

	got := status()
	if !strings.Contains(got, "running") {
		t.Fatalf("daemon status should report running:\n%s", got)
	}
	if !strings.Contains(got, pod) {
		t.Fatalf("daemon status should list the active pod:\n%s", got)
	}

	// `poddle stats` shows the running pod's live resource usage.
	statsCmd := exec.Command(bin, "stats")
	statsCmd.Env = env
	statsOut, err := statsCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stats failed: %v\n%s", err, statsOut)
	}
	if !strings.Contains(string(statsOut), pod) || !strings.Contains(string(statsOut), "%") {
		t.Errorf("stats should show %q with a percent usage:\n%s", pod, statsOut)
	}

	down := exec.Command(bin, "down", pod)
	down.Env = env
	if out, err := down.CombinedOutput(); err != nil {
		t.Fatalf("down failed: %v\n%s", err, out)
	}

	if after := status(); strings.Contains(after, pod) {
		t.Errorf("pod should be gone from status after down:\n%s", after)
	}
}
