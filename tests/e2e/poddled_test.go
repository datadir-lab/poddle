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
)

// TestE2E_Poddled_DetachRunDown proves the persistent broker: `up --detach`
// leaves a secretless pod running with its broker alive in poddled (the up
// process has exited); a SEPARATE `poddle run` then reaches the service through
// that still-alive broker; `down` tears it all down.
func TestE2E_Poddled_DetachRunDown(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var mu sync.Mutex
	var auths []string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
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
		"image = \"docker.io/library/node:22\"\nconnectors = [\"svc\"]\n")

	// Isolate this test's daemon socket under a throwaway XDG_RUNTIME_DIR.
	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		"XDG_RUNTIME_DIR="+filepath.Join(xdg, "run"))

	pod := "poddle-poddled-loop"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("pkill", "-f", "daemon --socket").Run() // best-effort: stop the daemon
	})

	// 1. up --detach: create the pod, auto-start poddled, issue the handle, exit.
	up := exec.Command(bin, "up", pod, "--detach")
	up.Dir, up.Env = proj, env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	// 2. run (a separate process — up has already exited): hit the service
	// through the still-alive broker. The whole command is one arg so the pod's
	// sh sees it verbatim and expands the connector env.
	runCmd := `curl -s -o /dev/null -H "Authorization: Bearer $WOODPECKER_TOKEN" "$WOODPECKER_SERVER/api/user" || true; echo RUNDONE`
	run := exec.Command(bin, "run", pod, runCmd)
	run.Env = env
	rout, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, rout)
	}
	if !strings.Contains(string(rout), "RUNDONE") {
		t.Fatalf("run command did not execute:\n%s", rout)
	}

	// 3. The broker persisted across `up` exiting: the mock saw the real token.
	mu.Lock()
	got := append([]string(nil), auths...)
	mu.Unlock()
	if len(got) == 0 {
		t.Fatalf("upstream received no request — broker did not persist after up exited")
	}
	sawReal := false
	for _, a := range got {
		if strings.Contains(a, "poddle_") {
			t.Errorf("handle leaked to upstream: %q", a)
		}
		if a == "Bearer SENTINEL" {
			sawReal = true
		}
	}
	if !sawReal {
		t.Errorf("upstream never saw the real token; got %v", got)
	}

	// 4. down: revoke + remove. The pod should be gone afterwards.
	down := exec.Command(bin, "down", pod)
	down.Env = env
	if out, err := down.CombinedOutput(); err != nil {
		t.Fatalf("down failed: %v\n%s", err, out)
	}
	ps, _ := exec.Command("podman", "ps", "-a", "--format", "{{.Names}}").CombinedOutput()
	if strings.Contains(string(ps), pod) {
		t.Errorf("pod still present after down:\n%s", ps)
	}
}
