//go:build e2e

package e2e

import (
	"fmt"
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

// upCase is one provider/harness the full-up secretless e2e can exercise.
// Adding a provider (openai/codex, aider, …) is a new row here — plus its own
// mock upstream. Selected at run time by PODDLE_E2E_PROVIDERS.
type upCase struct {
	name        string                                                               // subtest name / PODDLE_E2E_PROVIDERS key
	provider    string                                                               // identity provider (meta.toml)
	harness     string                                                               // --harness
	image       string                                                               // --image (must carry the harness's package manager)
	tokenFile   string                                                               // identity token filename the provider reads
	upstreamEnv string                                                               // env var overriding the provider's upstream to the mock
	podTokenEnv string                                                               // pod env var that must hold the handle, never the secret
	inPod       string                                                               // command run in the pod via --exec (must print "works")
	want        string                                                               // success substring in `poddle up --exec` output
	mock        func(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server // provider-shaped mock upstream
}

var upCases = []upCase{
	{
		name:        "anthropic",
		provider:    "anthropic",
		harness:     "claude-code",
		image:       "docker.io/library/node:22",
		tokenFile:   "anthropic-token",
		upstreamEnv: "PODDLE_ANTHROPIC_BASE_URL",
		podTokenEnv: "ANTHROPIC_AUTH_TOKEN",
		inPod: `export IS_SANDBOX=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1; ` +
			`echo '{"hasCompletedOnboarding":true,"theme":"dark"}' > $HOME/.claude.json; ` +
			`claude -p "ping" --output-format json --max-turns 1 --dangerously-skip-permissions </dev/null`,
		// The broker is a container and dials the upstream itself, so the mock
		// binds 0.0.0.0 and is reached at host.containers.internal (see runUpCase),
		// not the loopback mock.URL.
		mock: func(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
			return mockAnthropicUpOn(t, "0.0.0.0:0", auths, mu)
		},
	},
	{
		name:        "openai",
		provider:    "openai",
		harness:     "codex",
		image:       "docker.io/library/node:22",
		tokenFile:   "openai-token",
		upstreamEnv: "PODDLE_OPENAI_BASE_URL",
		podTokenEnv: "OPENAI_API_KEY",
		// The codex harness no longer writes $CODEX_HOME/config.toml — the broker
		// provider now rides `-c model_providers.poddle...` overrides on `codex exec`
		// (see codexProviderFlags in the codex harness) instead. --skip-git-repo-check
		// because the pod workdir may not be a git repo. The prompt is irrelevant — the
		// mock always replies with codexMarker.
		inPod: `codex exec --skip-git-repo-check 'reply'`,
		want:  codexMarker,
		mock:  mockOpenAIUp,
	},
	{
		name:        "aider",
		provider:    "openai", // reuses the openai provider
		harness:     "aider",
		image:       "docker.io/library/python:3.12", // full image: python 3.12 + git (aider needs 3.10-3.12, not 3.13)
		tokenFile:   "openai-token",
		upstreamEnv: "PODDLE_OPENAI_BASE_URL",
		podTokenEnv: "OPENAI_API_KEY",
		// aider is installed by the harness Provisions (pip); Env sets OPENAI_API_BASE to
		// the broker, so this one-shot routes pod->broker->mock. The prompt is irrelevant
		// (mock always replies aiderMarker).
		inPod: `aider --message 'reply' --yes-always --no-stream --no-pretty --no-check-update --no-analytics --model gpt-4o`,
		want:  aiderMarker,
		mock:  mockOpenAIChatUp,
	},
}

// selectUpCases returns the cases named in want (comma-separated), or all when
// want is empty. Errors on an unknown name (catches typos in the flag).
func selectUpCases(want string) ([]upCase, error) {
	if strings.TrimSpace(want) == "" {
		return upCases, nil
	}
	byName := make(map[string]upCase, len(upCases))
	for _, c := range upCases {
		byName[c.name] = c
	}
	var out []upCase
	for _, n := range strings.Split(want, ",") {
		if n = strings.TrimSpace(n); n == "" {
			continue
		}
		c, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("unknown provider %q; defined: %v", n, upCaseNames())
		}
		out = append(out, c)
	}
	return out, nil
}

func upCaseNames() []string {
	names := make([]string, len(upCases))
	for i, c := range upCases {
		names[i] = c.name
	}
	return names
}

// TestUpCaseSelection covers the PODDLE_E2E_PROVIDERS flag parsing (no podman).
func TestUpCaseSelection(t *testing.T) {
	if all, err := selectUpCases(""); err != nil || len(all) != len(upCases) {
		t.Errorf("empty → all: got %d, %v", len(all), err)
	}
	if one, err := selectUpCases(" anthropic "); err != nil || len(one) != 1 || one[0].name != "anthropic" {
		t.Errorf("named subset: got %+v, %v", one, err)
	}
	if _, err := selectUpCases("nope"); err == nil {
		t.Error("expected an error for an unknown provider")
	}
}

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

// codexMarker is the assistant text the mock emits — distinctive so a success
// assertion can't be satisfied by Codex echoing the prompt (Task 1 flagged that
// the prompt echo would false-positive a naive "works" check).
const codexMarker = "PODDLECODEXOK"

// mockOpenAIUp records the Authorization header of every request and returns the
// minimal Responses-API SSE stream `codex exec` needs (two events: output_item.done
// then response.completed), shape captured in the Task 1 spike. Binds 0.0.0.0.
func mockOpenAIUp(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		mu.Unlock()
		if !strings.Contains(r.URL.Path, "responses") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		item := `{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"` + codexMarker + `","annotations":[]}]}`
		fmt.Fprintf(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n", item)
		fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"output\":[%s],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n", item)
		if fl != nil {
			fl.Flush()
		}
	}))
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind 0.0.0.0: %v", err)
	}
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// aiderMarker is the assistant text the mock returns — distinctive so the success
// check can't be satisfied by aider echoing the prompt.
const aiderMarker = "PODDLEAIDEROK"

// mockOpenAIChatUp records the Authorization header and returns a minimal plain-JSON
// OpenAI chat.completions response (aider runs with --no-stream, so JSON not SSE).
// Binds 0.0.0.0 so the broker container reaches it at host.containers.internal.
func mockOpenAIChatUp(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-mock","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"` + aiderMarker + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind 0.0.0.0: %v", err)
	}
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// TestE2E_Up_Secretless drives the REAL `poddle up --identity --exec` against
// podman for each selected provider: pod create + Setup (harness install) +
// broker + the harness runs through the broker to a mock upstream. Proves the
// whole CLI path — the pod gets only a handle, the broker swaps it for the
// (sentinel) secret on the wire — with no real account. Select providers with
// PODDLE_E2E_PROVIDERS (default: all defined).
func TestE2E_Up_Secretless(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	cases, err := selectUpCases(os.Getenv("PODDLE_E2E_PROVIDERS"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runUpCase(t, bin, tc) })
	}
}

func runUpCase(t *testing.T, bin string, tc upCase) {
	var mu sync.Mutex
	var auths []string
	mock := tc.mock(t, &auths, &mu)
	// The broker container dials the upstream, so address the mock at
	// host.containers.internal (it binds 0.0.0.0), not the loopback mock.URL.
	_, mockPort, err := net.SplitHostPort(mock.Listener.Addr().String())
	if err != nil {
		t.Fatalf("mock addr: %v", err)
	}
	mockURL := "http://host.containers.internal:" + mockPort

	sentinel := "SENTINEL-UP-" + tc.name

	// A sentinel identity in a throwaway config dir — no interactive auth.
	cfg := t.TempDir()
	idDir := filepath.Join(cfg, "poddle", "identities", "work")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := "name = \"work\"\nprovider = \"" + tc.provider + "\"\n"
	if err := os.WriteFile(filepath.Join(idDir, "meta.toml"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, tc.tokenFile), []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	pod := "poddle-up-e2e-" + tc.name
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run() // fresh broker per test (its state dir is this test's temp)
	})

	cmd := exec.Command(bin, "up", pod,
		"--identity", "work",
		"--harness", tc.harness,
		"--image", tc.image,
		"--exec", tc.inPod,
	)
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+cfg,
		tc.upstreamEnv+"="+mockURL,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("poddle up --exec failed: %v\n%s", err, out)
	}
	want := tc.want
	if want == "" {
		want = `"result":"works"` // back-compat default for anthropic
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("%s did not return the success marker %q through the broker:\n%s", tc.harness, want, out)
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
	envOut, err := exec.Command("podman", "exec", pod, "sh", "-c", "echo $"+tc.podTokenEnv).CombinedOutput()
	if err != nil {
		t.Fatalf("pod env check: %v\n%s", err, envOut)
	}
	tok := strings.TrimSpace(string(envOut))
	if !strings.HasPrefix(tok, "poddle_") {
		t.Errorf("pod %s = %q, want a poddle_ handle", tc.podTokenEnv, tok)
	}
	if strings.Contains(tok, sentinel) {
		t.Errorf("real secret leaked into the pod env: %q", tok)
	}
}
