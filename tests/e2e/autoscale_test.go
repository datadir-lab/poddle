//go:build e2e

package e2e

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestE2E_Autoscale_WarnsInteractive proves the warn path end to end. An
// interactive pod opts in with --autoscale; we feed the daemon synthetic memory
// pressure (the PODDLE_AUTOSCALE_STATS seam, since rootless nested podman has no
// cgroup for real `podman stats`), and the daemon surfaces a warning in
// `poddle daemon status` WITHOUT moving the pod (a human is attached).
func TestE2E_Autoscale_WarnsInteractive(t *testing.T) {
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

	statsFile := filepath.Join(xdg, "stats.json")
	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		"XDG_RUNTIME_DIR="+filepath.Join(xdg, "run"),
		"PODDLE_AUTOSCALE_STATS="+statsFile,
		"PODDLE_AUTOSCALE_INTERVAL=1s")

	pod := "poddle-warn-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("pkill", "-f", "daemon --socket").Run()
	})

	up := exec.Command(bin, "up", pod, "--autoscale", "--detach")
	up.Dir, up.Env = proj, env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --autoscale failed: %v\n%s", err, out)
	}
	id0 := inspectField(pod, "{{.Id}}")

	// Feed sustained pressure for the interactive pod.
	writeFile(t, statsFile,
		`[{"name":"`+pod+`","mode":"interactive","size":"weak","memPercent":95}]`)

	status := func() string {
		s := exec.Command(bin, "daemon", "status")
		s.Env = env
		out, _ := s.CombinedOutput()
		return string(out)
	}
	var got string
	for i := 0; i < 30; i++ {
		got = status()
		if strings.Contains(got, "autoscale:") && strings.Contains(got, pod) {
			break
		}
		time.Sleep(time.Second)
	}
	if !strings.Contains(got, "autoscale:") || !strings.Contains(got, pod) {
		t.Fatalf("daemon status should warn about the pressured interactive pod:\n%s", got)
	}
	// It must NOT have been moved (interactive pods are warn-only).
	if id1 := inspectField(pod, "{{.Id}}"); id1 != id0 {
		t.Errorf("interactive pod must not be auto-moved (id %q -> %q)", id0, id1)
	}
}

// TestE2E_Autoscale_GrowsHeadless proves the grow path end to end. A headless
// task pod opts in with --autoscale; we feed the daemon synthetic memory
// pressure, and the daemon autonomously fires a real `poddle move` that
// recreates the pod on a bigger (strong) shell — no human in the loop.
func TestE2E_Autoscale_GrowsHeadless(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var mu sync.Mutex
	var auths []string
	mock := mockAnthropicUp(t, &auths, &mu)
	cfg := taskIdentity(t, "SENTINEL-AUTOSCALE")

	statsFile := filepath.Join(cfg, "stats.json")
	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+cfg,
		"XDG_RUNTIME_DIR="+filepath.Join(cfg, "run"),
		"PODDLE_ANTHROPIC_BASE_URL="+mock.URL,
		"PODDLE_AUTOSCALE_STATS="+statsFile,
		"PODDLE_AUTOSCALE_INTERVAL=1s")

	pod := "poddle-grow-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_, _ = exec.Command("sh", "-c",
			"podman volume ls -q --filter label=poddle.pod="+pod+" | xargs -r podman volume rm").CombinedOutput()
		_ = exec.Command("pkill", "-f", "daemon --socket").Run()
	})

	// A headless, autoscale-opted-in task pod (starts weak).
	task := exec.Command(bin, "task", "ping",
		"--identity", "work", "--image", "docker.io/library/node:22",
		"--name", pod, "--max-turns", "1", "--detach", "--autoscale")
	task.Env = env
	if out, err := task.CombinedOutput(); err != nil {
		t.Fatalf("task --autoscale --detach failed: %v\n%s", err, out)
	}
	// Wait for the first run to finish so the pod + state exist.
	for i := 0; i < 60; i++ {
		o, _ := exec.Command(bin, "logs", pod).CombinedOutput()
		if strings.Contains(string(o), `"result":"works"`) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if inspectField(pod, "{{.Id}}") == "" {
		t.Fatal("task pod was not created")
	}

	// Feed sustained pressure — the daemon should grow it onto a fresh strong
	// shell. The move recreates the container (size is a create-time label), so
	// once the pod is back at size=strong the autonomous grow has happened.
	writeFile(t, statsFile,
		`[{"name":"`+pod+`","mode":"headless","size":"weak","memPercent":95}]`)

	grown := false
	for i := 0; i < 60; i++ { // the autonomous move recreates + reinstalls the harness (~60s)
		if inspectField(pod, `{{index .Config.Labels "poddle.size"}}`) == "strong" {
			grown = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	_ = os.Remove(statsFile) // stop further moves (the file still says weak)
	if !grown {
		out, _ := exec.Command(bin, "daemon", "status").CombinedOutput()
		t.Fatalf("autoscaler never grew the pressured headless pod to strong; daemon status:\n%s", out)
	}
}

// inspectField returns a single podman-inspect Go-template field for a pod, or
// "" when the pod does not exist (e.g. briefly, mid-move). It reads only stdout
// so a "no such object" error never masquerades as a value.
func inspectField(pod, format string) string {
	out, err := exec.Command("podman", "inspect", "-f", format, pod).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
