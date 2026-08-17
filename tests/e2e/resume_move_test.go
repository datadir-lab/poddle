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

// TestE2E_ResumeMove_Headless proves resume-on-move end to end. A detached
// (headless) `poddle task` runs a coding agent whose conversation persists on a
// named state volume; `poddle move` recreates the shell on the carried-over
// volumes and, seeing the pod's `headless` mode, auto-resumes the agent — which
// reaches the mock Anthropic upstream again through the broker. We assert the
// move landed on a NEW container that kept the `headless` label and that the
// resumed agent re-hit the upstream (never leaking the handle).
func TestE2E_ResumeMove_Headless(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var mu sync.Mutex
	var auths []string
	mock := mockAnthropicUp(t, &auths, &mu)
	const sentinel = "SENTINEL-RESUME"
	cfg := taskIdentity(t, sentinel)

	// A project template so BOTH task and move resolve the same node image +
	// identity (move re-installs the harness + re-brokers on the fresh shell).
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\nidentity = \"work\"\n")

	pod := "poddle-resume-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_, _ = exec.Command("sh", "-c",
			"podman volume ls -q --filter label=poddle.pod="+pod+" | xargs -r podman volume rm").CombinedOutput()
		_ = exec.Command("pkill", "-f", "daemon --socket").Run()
	})

	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+cfg,
		"XDG_RUNTIME_DIR="+filepath.Join(cfg, "run"),
		"PODDLE_ANTHROPIC_BASE_URL="+mock.URL)

	run := func(args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir, c.Env = proj, env
		out, err := c.CombinedOutput()
		return string(out), err
	}
	inspect := func(format string) string {
		out, _ := exec.Command("podman", "inspect", "-f", format, pod).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	countAuths := func() int { mu.Lock(); defer mu.Unlock(); return len(auths) }

	// 1) Detached headless task — the agent runs and its conversation persists
	//    on the pod's /root/.claude state volume.
	if out, err := run("task", "ping", "--name", pod, "--max-turns", "1", "--detach"); err != nil {
		t.Fatalf("task --detach failed: %v\n%s", err, out)
	}
	var firstLogs string
	for i := 0; i < 60; i++ { // wait for the first run to finish
		firstLogs, _ = run("logs", pod)
		if strings.Contains(firstLogs, `"result":"works"`) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !strings.Contains(firstLogs, `"result":"works"`) {
		t.Fatalf("first task run never completed; logs:\n%s", firstLogs)
	}
	id1 := inspect("{{.Id}}")
	if got := inspect(`{{index .Config.Labels "poddle.mode"}}`); got != "headless" {
		t.Fatalf("task pod should be labeled headless; got %q", got)
	}
	baseline := countAuths()

	// 2) Move to a bigger shell — headless mode auto-resumes the agent.
	if out, err := run("move", pod, "--size", "strong"); err != nil {
		t.Fatalf("move failed: %v\n%s", err, out)
	}
	id2 := inspect("{{.Id}}")
	if id1 == "" || id1 == id2 {
		t.Fatalf("move should recreate the shell (id1=%q id2=%q)", id1, id2)
	}
	if got := inspect(`{{index .Config.Labels "poddle.mode"}}`); got != "headless" {
		t.Errorf("moved shell should stay headless; got %q", got)
	}

	// 3) The resumed agent reached the mock again through the broker.
	resumed := false
	for i := 0; i < 60; i++ {
		if countAuths() > baseline {
			resumed = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !resumed {
		last, _ := run("logs", pod)
		t.Fatalf("resume-on-move never re-hit the upstream (baseline=%d now=%d); logs:\n%s",
			baseline, countAuths(), last)
	}

	// The handle never leaked to the upstream; the broker swapped in the secret.
	mu.Lock()
	defer mu.Unlock()
	sawSentinel := false
	for _, a := range auths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("the handle leaked to the upstream: %q", a)
		}
		if a == "Bearer "+sentinel {
			sawSentinel = true
		}
	}
	if !sawSentinel {
		t.Errorf("upstream never saw the sentinel secret; got %v", auths)
	}
}
