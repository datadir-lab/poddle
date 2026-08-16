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

// mockService records the Authorization header of every request (the client
// will fail parsing the 200, but the request + auth are what we assert).
func mockService(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
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

type connCase struct {
	name      string
	connector string
	user      string                      // basic user (empty for bearer)
	inPod     func(mockURL string) string // in-pod command that hits the service through the broker
	wantAuth  func(sentinel string) string
}

var connCases = []connCase{
	{
		name: "forgejo_git", connector: "forgejo", user: "me",
		inPod: func(m string) string {
			return "git clone " + m + "/datadir/r.git /tmp/r 2>/dev/null || true; echo DONE"
		},
		wantAuth: func(s string) string { return "Basic " + base64.StdEncoding.EncodeToString([]byte("me:"+s)) },
	},
	{
		name: "npm", connector: "npm",
		inPod:    func(_ string) string { return "npm view express version 2>/dev/null || true; echo DONE" },
		wantAuth: func(s string) string { return "Bearer " + s },
	},
	{
		name: "woodpecker", connector: "woodpecker",
		inPod: func(_ string) string {
			return `curl -s -o /dev/null -H "Authorization: Bearer $WOODPECKER_TOKEN" "$WOODPECKER_SERVER/api/user" || true; echo DONE`
		},
		wantAuth: func(s string) string { return "Bearer " + s },
	},
}

// TestE2E_Connectors drives real `poddle up` with each connector against podman:
// the service request routes through the broker, which swaps the handle for the
// real token — the upstream sees the real auth, never the handle.
func TestE2E_Connectors(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)
	for _, tc := range connCases {
		t.Run(tc.name, func(t *testing.T) { runConnCase(t, bin, tc) })
	}
}

func runConnCase(t *testing.T, bin string, tc connCase) {
	var mu sync.Mutex
	var auths []string
	mock := mockService(t, &auths, &mu)
	const sentinel = "SENTINEL-CONN"

	// A connection of this connector, upstream = the mock, in a throwaway dir.
	xdg := t.TempDir()
	connDir := filepath.Join(xdg, "poddle", "connections", "svc")
	meta := "connector = \"" + tc.connector + "\"\nbase_url = \"" + mock.URL + "\"\nowner = \"local\"\n"
	if tc.user != "" {
		meta += "user = \"" + tc.user + "\"\n"
	}
	writeFile(t, filepath.Join(connDir, "meta.toml"), meta)
	writeFile(t, filepath.Join(connDir, tc.connector+"-token"), sentinel)

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\nconnectors = [\"svc\"]\n")

	pod := "poddle-conn-" + tc.name
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", pod).Run() })

	cmd := exec.Command(bin, "up", pod, "--exec", tc.inPod(mock.URL))
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("poddle up (%s) failed: %v\n%s", tc.connector, err, out)
	}
	if !strings.Contains(string(out), "DONE") {
		t.Fatalf("in-pod step did not run:\n%s", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(auths) == 0 {
		t.Fatalf("%s upstream received no requests — did not route through the broker:\n%s", tc.connector, out)
	}
	want := tc.wantAuth(sentinel)
	saw := false
	for _, a := range auths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("the handle leaked to the upstream: %q", a)
		}
		if a == want {
			saw = true
		}
	}
	if !saw {
		t.Errorf("%s upstream never saw %q; got %v", tc.connector, want, auths)
	}
}
