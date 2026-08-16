//go:build e2e

package broker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// claudeRunScript installs Claude Code in a fresh node container and runs it once
// headless against whatever ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN it is given.
// stdin is redirected from /dev/null because `-p` otherwise blocks reading stdin.
const claudeRunScript = `
set -e
echo '{"hasCompletedOnboarding":true,"theme":"dark"}' > /root/.claude.json
npm i -g @anthropic-ai/claude-code >/tmp/npm.log 2>&1 || { echo NPM_FAIL; tail -20 /tmp/npm.log; exit 1; }
timeout 60 claude -p "ping" --output-format json --max-turns 1 --dangerously-skip-permissions </dev/null
`

// mockAnthropic is an httptest server that records the Authorization header of
// every request and streams a minimal valid Messages SSE reply ("works").
func mockAnthropic(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		ev("message_start", `{"type":"message_start","message":{"id":"msg_e2e","type":"message","role":"assistant","model":"claude-opus-4-8","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`)
		ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"works"}}`)
		ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
		ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`)
		ev("message_stop", `{"type":"message_stop"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestE2E_Secretless_RealClaudeCode runs the REAL Claude Code CLI in a container
// pointed at a real broker that holds a sentinel secret. It proves the security
// property end to end with the actual client (not a Go stand-in): the pod is
// given only a handle, the broker swaps it for the real secret on the wire, so
// the upstream sees the secret and never the handle — and Claude Code completes.
//
// Needs docker; pulls node:22 and installs claude-code, so it is e2e-tagged.
func TestE2E_Secretless_RealClaudeCode(t *testing.T) {
	// docker locally; podman in CI (nested, so the container reaches the broker
	// via host.containers.internal — the real poddle path).
	cli := os.Getenv("PODDLE_E2E_CONTAINER_CLI")
	if cli == "" {
		cli = "docker"
	}
	if _, err := exec.LookPath(cli); err != nil {
		t.Skipf("%s not installed; skipping", cli)
	}

	var mu sync.Mutex
	var auths []string
	mock := mockAnthropic(t, &auths, &mu)

	const sentinel = "SENTINEL-REAL-TOKEN-e2e"
	b := NewBroker()
	credID, err := b.Store(Credential{Mode: ModeSubscription, Vendor: "anthropic", Secret: sentinel, BaseURL: mock.URL})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := b.IssueHandle(credID, "e2e", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := b.Serve("0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background()) })
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	// How the claude container reaches the host-bound broker. Locally (Docker
	// Desktop) that's the host alias. In CI the broker listens inside the step
	// container, so the claude container shares the step's netns
	// (PODDLE_E2E_DOCKER_NETWORK=container:<step>) and reaches it on 127.0.0.1
	// (PODDLE_E2E_BROKER_HOST=127.0.0.1).
	host := os.Getenv("PODDLE_E2E_BROKER_HOST")
	if host == "" {
		host = "host.docker.internal"
	}
	brokerURL := "http://" + net.JoinHostPort(host, port)

	args := []string{"run", "--rm", "-i", "--add-host", host + ":host-gateway"}
	if extraNet := os.Getenv("PODDLE_E2E_DOCKER_NETWORK"); extraNet != "" {
		args = append(args, "--network", extraNet)
	}
	args = append(args,
		"-e", "ANTHROPIC_BASE_URL="+brokerURL,
		"-e", "ANTHROPIC_AUTH_TOKEN="+handle.Value, // the HANDLE, never the secret
		"-e", "IS_SANDBOX=1",
		"-e", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"docker.io/library/node:22", "bash", "-s") // fully-qualified for podman
	cmd := exec.Command(cli, args...)
	cmd.Stdin = strings.NewReader(claudeRunScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude container failed: %v\n%s", err, out)
	}

	// 1. Claude Code completed the request THROUGH the broker.
	if !strings.Contains(string(out), `"result":"works"`) {
		t.Fatalf("claude did not return works through the broker:\n%s", out)
	}

	// 2. The upstream saw the REAL secret and NEVER the handle.
	mu.Lock()
	defer mu.Unlock()
	if len(auths) == 0 {
		t.Fatal("upstream received no requests")
	}
	sawSecret := false
	for _, a := range auths {
		if strings.Contains(a, handle.Value) {
			t.Errorf("the handle leaked to the upstream: %q", a)
		}
		if a == "Bearer "+sentinel {
			sawSecret = true
		}
	}
	if !sawSecret {
		t.Errorf("upstream never saw the real secret as a Bearer token; got %v", auths)
	}
}
