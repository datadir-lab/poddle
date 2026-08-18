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

// TestE2E_Audit_RecordsBrokeredAccess proves the audit spine end to end. A real
// `poddle task` runs a coding agent that reaches a mock upstream through the
// broker; afterwards `poddle daemon audit` shows the security events the daemon
// recorded — a handle.issue, the pod.task lifecycle, and the proxied request(s) —
// and the tamper-evident log never contains the secret or a handle.
func TestE2E_Audit_RecordsBrokeredAccess(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var mu sync.Mutex
	var auths []string
	mock := mockAnthropicUp(t, &auths, &mu)
	const sentinel = "SENTINEL-AUDIT"
	cfg := taskIdentity(t, sentinel)

	pod := "poddle-audit-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("pkill", "-f", "daemon --socket").Run()
	})

	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+cfg,
		"XDG_RUNTIME_DIR="+filepath.Join(cfg, "run"),
		"XDG_STATE_HOME="+filepath.Join(cfg, "state"), // isolates the audit db
		"PODDLE_ANTHROPIC_BASE_URL="+mock.URL)

	// A real agent run through the broker (tears the pod down after, leaving the
	// daemon — and its audit log — up).
	task := exec.Command(bin, "task", "ping",
		"--identity", "work", "--image", "docker.io/library/node:22",
		"--name", pod, "--max-turns", "1")
	task.Env = env
	if out, err := task.CombinedOutput(); err != nil {
		t.Fatalf("task failed: %v\n%s", err, out)
	}

	audit := exec.Command(bin, "daemon", "audit", "--limit", "200")
	audit.Env = env
	out, err := audit.CombinedOutput()
	if err != nil {
		t.Fatalf("daemon audit failed: %v\n%s", err, out)
	}
	s := string(out)
	for _, want := range []string{"request", "handle.issue", "pod.task"} {
		if !strings.Contains(s, want) {
			t.Errorf("audit log should contain a %q event:\n%s", want, s)
		}
	}
	// The tamper-evident log is secret-free: never the real token, never a handle.
	if strings.Contains(s, sentinel) {
		t.Errorf("audit log leaked the sentinel secret:\n%s", s)
	}
	if strings.Contains(s, "poddle_") {
		t.Errorf("audit log leaked a handle:\n%s", s)
	}

	// Filtering by pod works and still shows the request event.
	byPod := exec.Command(bin, "daemon", "audit", "--pod", pod, "--kind", "request")
	byPod.Env = env
	po, err := byPod.CombinedOutput()
	if err != nil {
		t.Fatalf("filtered audit failed: %v\n%s", err, po)
	}
	if !strings.Contains(string(po), "request") {
		t.Errorf("filtered audit (pod=%s kind=request) should show the request:\n%s", pod, po)
	}
}
