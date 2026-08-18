//go:build e2e

package e2e

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// mockOn starts a mock HTTP server bound to a specific loopback address, so two
// mocks can have distinct hosts (127.0.0.1 vs 127.0.0.2) for policy matching.
func mockOn(t *testing.T, addr string, hit *int32) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot bind %s (loopback alias unavailable): %v", addr, err)
	}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	_ = s.Listener.Close()
	s.Listener = ln
	s.Start()
	t.Cleanup(s.Close)
	return s
}

// TestE2E_ForwardProxy_GovernsArbitraryEgress proves forced egress end to end. A
// pod with a policy (allow only 127.0.0.1) has its ARBITRARY egress routed
// through the broker's forward proxy (HTTP_PROXY, set because it has a policy).
// Reaching an allow-listed host succeeds; reaching a disallowed host is blocked
// with 403 and never reaches the upstream.
func TestE2E_ForwardProxy_GovernsArbitraryEgress(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var connHit, allowHit, denyHit int32
	conn := mockOn(t, "127.0.0.1:0", &connHit)       // connector mock — spawns the daemon
	allowMock := mockOn(t, "127.0.0.1:0", &allowHit) // allow-listed host
	denyMock := mockOn(t, "127.0.0.2:0", &denyHit)   // different loopback host -> denied

	xdg := t.TempDir()
	connDir := filepath.Join(xdg, "poddle", "connections", "svc")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"woodpecker\"\nbase_url = \""+conn.URL+"\"\nowner = \"local\"\n")
	writeFile(t, filepath.Join(connDir, "woodpecker-token"), "SENTINEL")
	writeFile(t, filepath.Join(xdg, "poddle", "policies", "allowlist.toml"),
		"allow_upstreams = [\"127.0.0.1\"]\n")

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\nconnectors = [\"svc\"]\n")

	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		"XDG_RUNTIME_DIR="+filepath.Join(xdg, "run"),
		"XDG_STATE_HOME="+filepath.Join(xdg, "state"))

	pod := "poddle-fwd-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("pkill", "-f", "daemon --socket").Run()
	})

	up := exec.Command(bin, "up", pod, "--detach", "--policy", "allowlist")
	up.Dir, up.Env = proj, env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --policy failed: %v\n%s", err, out)
	}

	// `poddle run` already wraps the command in `sh -c`, so pass it as one arg.
	code := func(url string) string {
		r := exec.Command(bin, "run", pod, `curl -s -o /dev/null -w "%{http_code}" `+url)
		r.Env = env
		out, _ := r.CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	if c := code(allowMock.URL); !strings.Contains(c, "200") {
		t.Errorf("allow-listed egress should reach the upstream, got %q", c)
	}
	if c := code(denyMock.URL); !strings.Contains(c, "403") {
		t.Errorf("disallowed egress should be blocked with 403, got %q", c)
	}
	if atomic.LoadInt32(&denyHit) != 0 {
		t.Errorf("a policy-denied egress must never reach the upstream (hits=%d)", denyHit)
	}
	if atomic.LoadInt32(&allowHit) == 0 {
		t.Error("the allow-listed egress should have reached the upstream")
	}
}
