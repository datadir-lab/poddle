//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// taskIdentity writes a sentinel anthropic identity under cfg and returns cfg.
func taskIdentity(t *testing.T, sentinel string) string {
	t.Helper()
	cfg := t.TempDir()
	idDir := filepath.Join(cfg, "poddle", "identities", "work")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(idDir, "meta.toml"), "name = \"work\"\nprovider = \"anthropic\"\n")
	writeFile(t, filepath.Join(idDir, "anthropic-token"), sentinel)
	return cfg
}

// TestE2E_Task_RunsAgentHeadless drives the REAL `poddle task` against podman:
// it spins a fresh secretless pod, runs claude-code headless on a prompt, and
// the agent reaches a mock Anthropic upstream through the broker — returning
// "works" — then tears the pod down. No real account (sentinel + mock upstream).
func TestE2E_Task_RunsAgentHeadless(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var mu sync.Mutex
	var auths []string
	mock := mockAnthropicUp(t, &auths, &mu)
	const sentinel = "SENTINEL-TASK"

	cfg := t.TempDir()
	idDir := filepath.Join(cfg, "poddle", "identities", "work")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(idDir, "meta.toml"), "name = \"work\"\nprovider = \"anthropic\"\n")
	writeFile(t, filepath.Join(idDir, "anthropic-token"), sentinel)

	pod := "poddle-task-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("pkill", "-f", "daemon --socket").Run()
	})

	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+cfg,
		"PODDLE_SOCKET="+filepath.Join(cfg, "poddled.sock"),
		"PODDLE_ANTHROPIC_BASE_URL="+mock.URL)

	cmd := exec.Command(bin, "task", "ping",
		"--identity", "work",
		"--image", "docker.io/library/node:22",
		"--name", pod,
		"--max-turns", "1")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("poddle task failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"result":"works"`) {
		t.Fatalf("the agent did not return works through the broker:\n%s", out)
	}

	// The upstream saw the real (sentinel) secret, never the handle.
	mu.Lock()
	defer mu.Unlock()
	saw := false
	for _, a := range auths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("the handle leaked to the upstream: %q", a)
		}
		if a == "Bearer "+sentinel {
			saw = true
		}
	}
	if !saw {
		t.Errorf("upstream never saw the sentinel secret; got %v", auths)
	}

	// task tears the pod down by default.
	ps, _ := exec.Command("podman", "ps", "-a", "--format", "{{.Names}}").CombinedOutput()
	if strings.Contains(string(ps), pod) {
		t.Errorf("task should have removed the pod:\n%s", ps)
	}
}

// TestE2E_Task_DetachRunsInBackground proves `poddle task --detach`: the agent
// runs in the background, the command returns immediately leaving the pod up,
// `poddle logs` streams the result, and the run reached the mock through the
// broker.
func TestE2E_Task_DetachRunsInBackground(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var mu sync.Mutex
	var auths []string
	mock := mockAnthropicUp(t, &auths, &mu)
	const sentinel = "SENTINEL-TASKD"
	cfg := taskIdentity(t, sentinel)

	pod := "poddle-taskd-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("pkill", "-f", "daemon --socket").Run()
	})

	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+cfg,
		"PODDLE_SOCKET="+filepath.Join(cfg, "poddled.sock"),
		"PODDLE_ANTHROPIC_BASE_URL="+mock.URL)

	// --detach returns promptly, leaving the agent running in the background.
	cmd := exec.Command(bin, "task", "ping",
		"--identity", "work", "--image", "docker.io/library/node:22",
		"--name", pod, "--max-turns", "1", "--detach")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("poddle task --detach failed: %v\n%s", err, out)
	}

	// The pod is still up (detached did not tear it down).
	ps, _ := exec.Command("podman", "ps", "--format", "{{.Names}}").CombinedOutput()
	if !strings.Contains(string(ps), pod) {
		t.Fatalf("detached task should leave the pod running; ps:\n%s", ps)
	}

	// Poll `poddle logs` until the agent finishes.
	var logsOut string
	done := false
	for i := 0; i < 30; i++ {
		o, _ := exec.Command(bin, "logs", pod).CombinedOutput()
		logsOut = string(o)
		if strings.Contains(logsOut, `"result":"works"`) {
			done = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !done {
		t.Fatalf("detached task never completed; last logs:\n%s", logsOut)
	}

	// The upstream saw the real (sentinel) secret, never the handle.
	mu.Lock()
	defer mu.Unlock()
	saw := false
	for _, a := range auths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("the handle leaked to the upstream: %q", a)
		}
		if a == "Bearer "+sentinel {
			saw = true
		}
	}
	if !saw {
		t.Errorf("upstream never saw the sentinel secret; got %v", auths)
	}

	// Manual teardown works.
	down := exec.Command(bin, "down", pod)
	down.Env = env
	if out, err := down.CombinedOutput(); err != nil {
		t.Fatalf("down failed: %v\n%s", err, out)
	}
}
