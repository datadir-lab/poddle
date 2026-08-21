//go:build e2e

package e2e

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// mockAnthropicUpOn is mockAnthropicUp (secretless_up_test.go) bound to addr
// instead of httptest's default loopback-only listener. The broker itself
// holds the upstream credential and dials it, so when a brokered pod's
// harness reaches the mock THROUGH the containerized broker, the mock must be
// reachable at host.containers.internal — which requires binding 0.0.0.0.
func mockAnthropicUpOn(t *testing.T, addr string, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot bind %s: %v", addr, err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "count_tokens") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"input_tokens":1}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		fl, _ := w.(http.Flusher)
		ev := func(event, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			if fl != nil {
				fl.Flush()
			}
		}
		ev("message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-opus-4-8","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`)
		ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"works"}}`)
		ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
		ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`)
		ev("message_stop", `{"type":"message_stop"}`)
	}))
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// TestE2E_Audit_RecordsBrokeredAccess proves the audit spine end to end. A real
// `poddle task` runs a coding agent that reaches a mock upstream through the
// broker; afterwards `poddle daemon audit` shows the security events the daemon
// recorded — a handle.issue, the pod.task lifecycle, and the proxied request(s) —
// and the tamper-evident log never contains the secret or a handle.
//
// The broker is a container, so the mock is bound 0.0.0.0 and reached at
// host.containers.internal (the address the broker itself dials).
func TestE2E_Audit_RecordsBrokeredAccess(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var mu sync.Mutex
	var auths []string
	mock := mockAnthropicUpOn(t, "0.0.0.0:0", &auths, &mu)
	_, mockPort, err := net.SplitHostPort(mock.Listener.Addr().String())
	if err != nil {
		t.Fatalf("mock addr: %v", err)
	}
	mockURL := "http://host.containers.internal:" + mockPort
	const sentinel = "SENTINEL-AUDIT"
	cfg := taskIdentity(t, sentinel)

	pod := "poddle-audit-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
	})

	// Isolate only the CLI config; DO NOT repoint XDG_RUNTIME_DIR — rootless
	// podman needs the real one (its own socket + the broker container's pasta
	// networking), and the shared broker container is the intended model.
	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+cfg,
		"PODDLE_ANTHROPIC_BASE_URL="+mockURL)

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
