//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// mockAnthropicUp records the Authorization header of every request and streams
// a minimal valid Messages SSE reply ("works").
func mockAnthropicUp(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
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
		ev("message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-opus-4-8","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`)
		ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"works"}}`)
		ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
		ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`)
		ev("message_stop", `{"type":"message_stop"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestE2E_Up_Secretless drives the REAL `poddle up --identity --exec` against
// podman: the pod is created, claude-code is installed via Setup, and Claude
// Code runs through the broker to a mock upstream. It proves the whole CLI
// path end-to-end — the pod is handed only a handle, the broker swaps it for
// the real (sentinel) secret on the wire, reachable via host.containers.internal
// under real podman — with no real Anthropic account (sentinel token + mock).
func TestE2E_Up_Secretless(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var mu sync.Mutex
	var auths []string
	mock := mockAnthropicUp(t, &auths, &mu)

	const sentinel = "SENTINEL-UP-E2E"

	// A sentinel identity in a throwaway config dir — no `claude setup-token`.
	cfg := t.TempDir()
	idDir := filepath.Join(cfg, "poddle", "identities", "work")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "meta.toml"), []byte("name = \"work\"\nprovider = \"anthropic\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "anthropic-token"), []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	const pod = "poddle-up-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run() // clean slate
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", pod).Run() })

	// Runs inside the pod: seed onboarding + root-sandbox, then run claude once.
	inPod := `export IS_SANDBOX=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1; ` +
		`echo '{"hasCompletedOnboarding":true,"theme":"dark"}' > $HOME/.claude.json; ` +
		`claude -p "ping" --output-format json --max-turns 1 --dangerously-skip-permissions </dev/null`

	cmd := exec.Command(bin, "up", pod,
		"--identity", "work",
		"--harness", "claude-code",
		"--image", "docker.io/library/node:22",
		"--exec", inPod,
	)
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+cfg,
		"PODDLE_ANTHROPIC_BASE_URL="+mock.URL,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("poddle up --exec failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"result":"works"`) {
		t.Fatalf("claude did not return works through the broker:\n%s", out)
	}

	// The upstream saw the real secret and NEVER the handle.
	mu.Lock()
	defer mu.Unlock()
	sawSecret := false
	for _, a := range auths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("the handle leaked to the upstream: %q", a)
		}
		if a == "Bearer "+sentinel {
			sawSecret = true
		}
	}
	if !sawSecret {
		t.Errorf("upstream never saw the sentinel secret; got %v", auths)
	}

	// The pod env carried only the handle, never the secret.
	envOut, err := exec.Command("podman", "exec", pod, "sh", "-c", "echo $ANTHROPIC_AUTH_TOKEN").CombinedOutput()
	if err != nil {
		t.Fatalf("pod env check: %v\n%s", err, envOut)
	}
	tok := strings.TrimSpace(string(envOut))
	if !strings.HasPrefix(tok, "poddle_") {
		t.Errorf("pod ANTHROPIC_AUTH_TOKEN = %q, want a poddle_ handle", tok)
	}
	if strings.Contains(tok, sentinel) {
		t.Errorf("real secret leaked into the pod env: %q", tok)
	}
}
