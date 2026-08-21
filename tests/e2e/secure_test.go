//go:build e2e

package e2e

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestE2E_Secure_BlockPathRefusesMount: real `poddle up` refuses a mount that
// would expose a blocked host path. This fails before podman is touched.
func TestE2E_Secure_BlockPathRefusesMount(t *testing.T) {
	bin := buildBinary(t)

	proj := t.TempDir()
	secret := filepath.ToSlash(filepath.Join(proj, "secretstore"))
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\n"+
			"block_paths = [\""+secret+"\"]\n"+
			"[[mounts]]\nhost = \""+secret+"\"\ncontainer = \"/s\"\n")

	cmd := exec.Command(bin, "up", "poddle-sec-block")
	cmd.Dir = proj
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("up should have refused the blocked mount; output:\n%s", out)
	}
	if !strings.Contains(string(out), "blocked path") {
		t.Errorf("expected a blocked-path error, got:\n%s", out)
	}
}

// TestE2E_Secure_EgressRedactsSecret: a pod POSTs a secret through the broker;
// the broker's egress redaction scrubs it before it reaches the upstream.
func TestE2E_Secure_EgressRedactsSecret(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var mu sync.Mutex
	var bodies []string
	// The broker is a container, so the mock binds 0.0.0.0 and is dialed by the
	// broker at host.containers.internal, not 127.0.0.1.
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mock := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	_ = mock.Listener.Close()
	mock.Listener = ln
	mock.Start()
	t.Cleanup(mock.Close)

	_, mockPort, err := net.SplitHostPort(mock.Listener.Addr().String())
	if err != nil {
		t.Fatalf("mock addr: %v", err)
	}
	mockAddr := "http://host.containers.internal:" + mockPort

	// A bearer connector pointing at the mock gives the pod a broker'd endpoint.
	xdg := t.TempDir()
	connDir := filepath.Join(xdg, "poddle", "connections", "svc")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"woodpecker\"\nbase_url = \""+mockAddr+"\"\nowner = \"local\"\n")
	writeFile(t, filepath.Join(connDir, "woodpecker-token"), "SENTINEL")

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\nconnectors = [\"svc\"]\n")

	pod := "poddle-sec-egress"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run() // fresh broker per test (its state dir is this test's temp)
	})

	// Simulate an agent exfiltrating a secret it found: POST it as JSON.
	inpod := `curl -s -o /dev/null -X POST -H "Content-Type: application/json" ` +
		`-H "Authorization: Bearer $WOODPECKER_TOKEN" ` +
		`-d '{"leak":"AKIAIOSFODNN7EXAMPLE"}' "$WOODPECKER_SERVER/v1/x" || true; echo DONE`
	cmd := exec.Command(bin, "up", pod, "--exec", inpod)
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("poddle up failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "DONE") {
		t.Fatalf("in-pod step did not run:\n%s", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 {
		t.Fatalf("upstream received no request body:\n%s", out)
	}
	joined := strings.Join(bodies, "\n")
	if strings.Contains(joined, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("the secret reached the upstream unredacted: %q", joined)
	}
	if !strings.Contains(joined, "redacted:poddle") {
		t.Errorf("expected the redaction placeholder in the upstream body, got %q", joined)
	}
}
