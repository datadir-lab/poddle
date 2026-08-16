//go:build e2e

package e2e

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// mockGit records the Authorization header of every request (git will fail
// parsing the 200, but the request + auth are what we assert).
func mockGit(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestE2E_Connector_GitBasic drives the real `poddle up` with a forgejo
// connection against podman: the pod's git config is rewritten to the broker,
// and a `git clone` presents the handle as its Basic username. The broker swaps
// it for the real user:token — the mock upstream sees the real Basic creds and
// NEVER the handle, with no token in the pod.
func TestE2E_Connector_GitBasic(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var mu sync.Mutex
	var auths []string
	mock := mockGit(t, &auths, &mu)
	const sentinel = "SENTINEL-GIT"

	// A forgejo connection (upstream = the mock) in a throwaway config dir.
	xdg := t.TempDir()
	connDir := filepath.Join(xdg, "poddle", "connections", "my-forgejo")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"forgejo\"\nbase_url = \""+mock.URL+"\"\nuser = \"me\"\nowner = \"local\"\n")
	writeFile(t, filepath.Join(connDir, "forgejo-token"), sentinel)

	// A project template that brokers the connection into the pod.
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\nconnectors = [\"my-forgejo\"]\n")

	const pod = "poddle-conn-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", pod).Run() })

	// The pod's git config (set by the connector) rewrites the mock URL to the
	// broker; the clone fails parsing the mock's 200 (|| true), but the request
	// reaches the upstream with the swapped auth.
	inPod := "git clone " + mock.URL + "/datadir/r.git /tmp/r 2>/tmp/e || true; echo GIT_DONE"

	cmd := exec.Command(bin, "up", pod, "--exec", inPod)
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("poddle up (connector) failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "GIT_DONE") {
		t.Fatalf("in-pod git step did not run:\n%s", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(auths) == 0 {
		t.Fatalf("git upstream received no requests — the clone did not route through the broker:\n%s", out)
	}
	wantBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("me:"+sentinel))
	saw := false
	for _, a := range auths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("the handle leaked to the git upstream: %q", a)
		}
		if a == wantBasic {
			saw = true
		}
	}
	if !saw {
		t.Errorf("git upstream never saw the real Basic creds; got %v", auths)
	}
}
