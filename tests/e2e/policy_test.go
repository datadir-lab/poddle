//go:build e2e

package e2e

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestE2E_Policy_DeniesDisallowedUpstream proves policy enforcement end to end.
// A pod is created with a lockdown governance policy (allow only a bogus host, so
// every real upstream is default-denied); when the pod tries to reach its
// connector through the broker, the gateway denies it with 403, the mock upstream
// is never reached, and the denial is recorded in the audit log.
func TestE2E_Policy_DeniesDisallowedUpstream(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	// The gateway checks policy by hostname before it ever dials the connector's
	// base_url (see gateway.go ServeHTTP), so this mock is never reached and
	// needs no broker-reachable address — a plain loopback httptest.Server is
	// enough to prove hits stays 0.
	var hits int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(mock.Close)

	xdg := t.TempDir()
	connDir := filepath.Join(xdg, "poddle", "connections", "svc")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"woodpecker\"\nbase_url = \""+mock.URL+"\"\nowner = \"local\"\n")
	writeFile(t, filepath.Join(connDir, "woodpecker-token"), "SENTINEL")
	// Lockdown: allow only a bogus host -> every real upstream is default-denied.
	writeFile(t, filepath.Join(xdg, "poddle", "policies", "lockdown.toml"),
		"allow_upstreams = [\"never.example.invalid\"]\n")

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\nconnectors = [\"svc\"]\n")

	// Isolate only the CLI config; DO NOT repoint XDG_RUNTIME_DIR — rootless
	// podman needs the real one (its own socket + the broker container's pasta
	// networking), and the shared broker container is the intended model.
	env := append(os.Environ(), "XDG_CONFIG_HOME="+xdg)

	pod := "poddle-policy-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		down := exec.Command(bin, "down", pod)
		down.Env = env
		_ = down.Run() // disconnects the broker from the lock net, then removes the pod
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
	})

	// The pod reaches its connector through the broker; the policy must deny it.
	inPod := `curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $WOODPECKER_TOKEN" "$WOODPECKER_SERVER/api/user"`
	up := exec.Command(bin, "up", pod, "--policy", "lockdown", "--exec", inPod)
	up.Dir, up.Env = proj, env
	out, err := up.CombinedOutput()
	if err != nil {
		t.Fatalf("up --exec failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "403") {
		t.Fatalf("policy should deny the brokered request with 403; got:\n%s", out)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Errorf("a policy-denied request must never reach the upstream (hits=%d)", n)
	}

	// The denial is audited.
	au := exec.Command(bin, "daemon", "audit", "--decision", "deny")
	au.Env = env
	aout, _ := au.CombinedOutput()
	if !strings.Contains(string(aout), pod) || !strings.Contains(string(aout), "deny") {
		t.Errorf("the policy deny should be audited for %q:\n%s", pod, aout)
	}
}
