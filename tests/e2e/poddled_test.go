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

	// The mock is bound 0.0.0.0 and reached at host.containers.internal: `run`
	// curls $WOODPECKER_SERVER (the broker) FROM INSIDE the pod, and the broker
	// container itself dials this mock to relay the request.
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind 0.0.0.0:0: %v", err)
	}
	var mu sync.Mutex
	var auths []string
	mock := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
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
	mockURL := "http://host.containers.internal:" + mockPort

	xdg := t.TempDir()
	connDir := filepath.Join(xdg, "poddle", "connections", "svc")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"woodpecker\"\nbase_url = \""+mockURL+"\"\nowner = \"local\"\n")
	writeFile(t, filepath.Join(connDir, "woodpecker-token"), "SENTINEL")

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\nconnectors = [\"svc\"]\n")

	// Isolate only the CLI config; DO NOT repoint XDG_RUNTIME_DIR — rootless
	// podman needs the real one (its own socket + the broker container's pasta
	// networking), and the shared broker container is the intended model.
	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg)

	pod := "poddle-poddled-loop"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
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

	// 4. down: revoke + remove. The gateway/data-plane listener now lives INSIDE
	// the broker container on the pod's internal lock net, so it is no longer
	// reachable from the host at 127.0.0.1 — we can't dial it to check handle
	// validity (that host-side gwStatus probe is intentionally dropped). Validity
	// BEFORE down is already proven above: the `run` at step 2 reached the service
	// through the broker with the real token. Revocation AFTER down is asserted via
	// the daemon's audit log (handle.revoke), not a host-side gateway probe.
	down := exec.Command(bin, "down", pod)
	down.Env = env
	if out, err := down.CombinedOutput(); err != nil {
		t.Fatalf("down failed: %v\n%s", err, out)
	}

	// down revoked the pod's handles at the (still-running) daemon: the audit log
	// records a handle.revoke for the pod.
	rev := exec.Command(bin, "daemon", "audit", "--pod", pod)
	rev.Env = env
	revOut, _ := rev.CombinedOutput()
	if !strings.Contains(string(revOut), "handle.revoke") {
		t.Errorf("down should revoke the pod's handles (want a handle.revoke audit event for %q):\n%s", pod, revOut)
	}

	// And the pod is gone.
	ps, _ := exec.Command("podman", "ps", "-a", "--format", "{{.Names}}").CombinedOutput()
	if strings.Contains(string(ps), pod) {
		t.Errorf("pod still present after down:\n%s", ps)
	}
}

// gwStatus GETs the gateway with a handle and returns the status code.
func gwStatus(t *testing.T, gwURL, handle string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", gwURL+"/", nil)
	req.Header.Set("Authorization", "Bearer "+handle)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("gateway request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
