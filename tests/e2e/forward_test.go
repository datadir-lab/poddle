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
// pod with a policy (allow only the broker-reachable mock host) has its
// ARBITRARY egress routed through the broker's forward proxy (HTTP_PROXY, set
// because it has a policy). Reaching an allow-listed host succeeds; reaching a
// disallowed host is blocked with 403 and never reaches the upstream.
//
// The broker is a container, so the allow-listed upstream is addressed via
// host.containers.internal (reachable from the broker's egress network) and the
// mock binds 0.0.0.0. The denied host is a bogus name the policy rejects
// *before* the broker dials, so it needs no server.
func TestE2E_ForwardProxy_GovernsArbitraryEgress(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var connHit, allowHit int32
	conn := mockOn(t, "127.0.0.1:0", &connHit) // connector mock — spawns the daemon, never dialed
	allowMock := mockOn(t, "0.0.0.0:0", &allowHit)
	_, allowPort, err := net.SplitHostPort(allowMock.Listener.Addr().String())
	if err != nil {
		t.Fatalf("allow mock addr: %v", err)
	}
	allowURL := "http://host.containers.internal:" + allowPort + "/"
	const denyURL = "http://blocked.invalid/"

	xdg := t.TempDir()
	connDir := filepath.Join(xdg, "poddle", "connections", "svc")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"woodpecker\"\nbase_url = \""+conn.URL+"\"\nowner = \"local\"\n")
	writeFile(t, filepath.Join(connDir, "woodpecker-token"), "SENTINEL")
	writeFile(t, filepath.Join(xdg, "poddle", "policies", "allowlist.toml"),
		"allow_upstreams = [\"host.containers.internal\"]\n")

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\nconnectors = [\"svc\"]\n")

	// Isolate only the CLI config; DO NOT repoint XDG_RUNTIME_DIR — rootless
	// podman needs the real one (its own socket + the broker container's pasta
	// networking), and the shared broker container is the intended model.
	env := append(os.Environ(), "XDG_CONFIG_HOME="+xdg)

	pod := "poddle-fwd-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		down := exec.Command(bin, "down", pod)
		down.Env = env
		_ = down.Run() // disconnects the broker from the lock net, then removes the pod
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run() // fresh broker per test (its state dir is this test's temp)
		_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
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
	if c := code(allowURL); !strings.Contains(c, "200") {
		t.Errorf("allow-listed egress should reach the upstream, got %q", c)
	}
	if c := code(denyURL); !strings.Contains(c, "403") {
		t.Errorf("disallowed egress should be blocked with 403, got %q", c)
	}
	if atomic.LoadInt32(&allowHit) == 0 {
		t.Error("the allow-listed egress should have reached the upstream")
	}
}
