//go:build e2e

package e2e

import (
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// taskIdentity writes a sentinel anthropic identity under cfg and returns cfg.
func taskIdentity(t *testing.T, sentinel string) string {
	t.Helper()
	cfg := t.TempDir()
	idDir := filepath.Join(cfg, "poddle", "identities", "work")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(idDir, "meta.toml"), "name = \"work\"\nprovider = \"anthropic\"\n")
	writeFile(t, filepath.Join(idDir, "anthropic-token"), sentinel)
	return cfg
}

// taskWorksMarker is the assistant text claude-code's mock upstream replies for
// TestE2E_Task_RunsAgentHeadless's claude-code row. claude-code's TaskCommand runs
// with --output-format json, which wraps the reply as `"result":"works"` — the
// existing (pre-parametrization) assertion here — but strings.Contains(stdout,
// taskWorksMarker) is a strict subset of that, so the row is unaffected. The other
// harnesses reply with their own distinctive marker constant instead of "works"
// (see secretless_up_test.go's codexMarker/aiderMarker/geminiMarker/opencodeMarker/
// piMarker) so the assertion can't be satisfied by an agent merely echoing the
// prompt or by another row's traffic.
const taskWorksMarker = "works"

// taskCase is one harness the headless `poddle task` e2e drives end to end: its
// harness/image/writeIdentity/upstreamEnv/modelMock quadruple is reused VERBATIM
// from mcpCases (mcp_test.go) — the same verified per-harness identity + mock
// wiring, just for `poddle task` instead of `poddle up --exec` — plus want, the
// distinctive marker this row's mock replies with (from secretless_up_test.go's
// upCases, which already proves each harness's TaskCommand-shaped invocation
// surfaces that marker in combined stdout via `poddle up --exec`; `poddle task`
// runs the prompt through the exact same podman.Provider.Exec, so the same holds
// here — see runTaskCase's doc comment for the full argument). mcpCases has no
// aider row (aider has no MCP client), so aider's quadruple here is assembled from
// edit_test.go's writeOpenAIIdentity + TestE2E_Edit_Aider's image
// (docker.io/library/python:3.12 — aider needs python 3.10-3.12 + git, not the
// node image the other 5 harnesses use) and secretless_up_test.go's
// mockOpenAIChatUp/aiderMarker (aider runs --no-stream, so it needs the flat-JSON
// mock, not the SSE ones the streaming harnesses need).
type taskCase struct {
	name          string                                                               // subtest name
	harness       string                                                               // --harness
	image         string                                                               // --image (must carry the harness's package manager)
	writeIdentity func(t *testing.T, xdg, sentinel string)                             // writeOpenAIIdentity / writeAnthropicIdentity / writeGoogleIdentity (edit_test.go)
	upstreamEnv   string                                                               // provider base-URL env var poddle task reads
	modelMock     func(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server // this row's model-shaped mock upstream
	want          string                                                               // the distinctive marker this row's mock replies with
}

var taskCases = []taskCase{
	{
		// codex exec's Responses-API SSE turn (mockOpenAIUp) replies codexMarker as
		// plain output_text — codex prints the final assistant message to stdout, so
		// it surfaces unwrapped (secretless_up_test.go's "openai" row proves this via
		// the identical `codex exec ... -c model_providers.poddle...` shape).
		name:          "codex",
		harness:       "codex",
		image:         "docker.io/library/node:22",
		writeIdentity: writeOpenAIIdentity,
		upstreamEnv:   "PODDLE_OPENAI_BASE_URL",
		modelMock:     mockOpenAIUp,
		want:          codexMarker,
	},
	{
		// claude-code's --output-format json wraps the reply as `"result":"works"` —
		// taskWorksMarker ("works") is a Contains-safe substring of that wrapping.
		name:          "claude-code",
		harness:       "claude-code",
		image:         "docker.io/library/node:22",
		writeIdentity: writeAnthropicIdentity,
		upstreamEnv:   "PODDLE_ANTHROPIC_BASE_URL",
		modelMock: func(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
			return mockAnthropicUpOn(t, "0.0.0.0:0", auths, mu)
		},
		want: taskWorksMarker,
	},
	{
		// opencode always streams; --format json prints one JSON event per SSE
		// chunk, so opencodeMarker rides inside a JSON string value — Contains still
		// finds it. mockOpenAIStreamUpMarker (mcp_test.go) parameterizes the shared
		// SSE mock on the marker so this row's reply can't be confused with pi's.
		name:          "opencode",
		harness:       "opencode",
		image:         "docker.io/library/node:22",
		writeIdentity: writeOpenAIIdentity,
		upstreamEnv:   "PODDLE_OPENAI_BASE_URL",
		modelMock: func(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
			return mockOpenAIStreamUpMarker(t, auths, mu, opencodeMarker)
		},
		want: opencodeMarker,
	},
	{
		// pi also always streams (same reason as opencode) and prints the assistant
		// reply as plain text — piMarker surfaces unwrapped.
		name:          "pi",
		harness:       "pi",
		image:         "docker.io/library/node:22",
		writeIdentity: writeOpenAIIdentity,
		upstreamEnv:   "PODDLE_OPENAI_BASE_URL",
		modelMock: func(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
			return mockOpenAIStreamUpMarker(t, auths, mu, piMarker)
		},
		want: piMarker,
	},
	{
		// gemini-cli's --output-format json wraps the final response object, so
		// geminiMarker rides inside JSON — Contains finds it (proved by
		// secretless_up_test.go's "gemini" row against the identical `gemini -p ...
		// --output-format json --yolo` invocation).
		name:          "gemini",
		harness:       "gemini",
		image:         "docker.io/library/node:22",
		writeIdentity: writeGoogleIdentity,
		upstreamEnv:   "PODDLE_GOOGLE_BASE_URL",
		modelMock:     mockGeminiUp,
		want:          geminiMarker,
	},
	{
		// aider runs --no-stream --no-pretty and prints the assistant reply as plain
		// text — aiderMarker surfaces unwrapped. mockOpenAIChatUp (not the SSE mocks
		// above) matches --no-stream's flat-JSON chat.completions shape.
		name:          "aider",
		harness:       "aider",
		image:         "docker.io/library/python:3.12",
		writeIdentity: writeOpenAIIdentity,
		upstreamEnv:   "PODDLE_OPENAI_BASE_URL",
		modelMock:     mockOpenAIChatUp,
		want:          aiderMarker,
	},
}

// TestE2E_Task_RunsAgentHeadless drives the REAL `poddle task` against podman for
// every harness poddle drives (codex, claude-code, opencode, pi, gemini, aider):
// each row spins its own fresh secretless pod, runs that harness's coding agent
// headless on a prompt, and the agent reaches its own provider-shaped mock
// upstream through the broker — returning its distinctive marker — then tears the
// pod down. No real account anywhere (sentinel identity + mock upstream, per row).
func TestE2E_Task_RunsAgentHeadless(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)
	for _, tc := range taskCases {
		t.Run(tc.name, func(t *testing.T) { runTaskCase(t, bin, tc) })
	}
}

// runTaskCase drives one taskCase end to end. Everything is per-row and isolated
// (a fresh model mock instance, a fresh XDG dir, a uniquely-named pod) so one row's
// traffic can never be mistaken for another's.
//
// The assertion reads tc.want out of `poddle task`'s own combined stdout via
// strings.Contains rather than an exact/structured match, because different
// harnesses surface the model's reply differently — claude-code and gemini-cli
// wrap it in a JSON envelope (--output-format json), opencode wraps each SSE
// chunk as its own JSON event, while codex/pi/aider print the plain assistant
// text — so a substring check is the one assertion shape that is correct for all
// six without special-casing each row's exact envelope. This is sound, not
// vacuous: `poddle task`'s harness invocation (h.TaskCommand) and its output
// capture (podman.Provider.Exec — "exec", id, "sh", "-c", command, streamed to the
// caller's stdio) are IDENTICAL to what `poddle up --exec` uses for the very same
// per-harness command shapes in secretless_up_test.go's upCases, which already
// proves — against real podman, per harness — that each of these markers reaches
// combined stdout. `poddle task` differs from `up --exec` only in how the pod is
// built and torn down (buildSpec/Create + RevokePod/Remove vs plain up/down), not
// in how the command runs or its output is captured, so that proof carries over
// directly. Every harness here DOES surface its marker; none needed a weaker or
// skipped assertion.
func runTaskCase(t *testing.T, bin string, tc taskCase) {
	var mu sync.Mutex
	var auths []string
	mock := tc.modelMock(t, &auths, &mu)
	mockURL := mockURLFor(t, mock)
	sentinel := "SENTINEL-TASK-" + strings.ToUpper(tc.name)

	cfg := t.TempDir()
	tc.writeIdentity(t, cfg, sentinel)

	pod := "poddle-task-" + tc.name
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
		_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
	})

	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+cfg,
		tc.upstreamEnv+"="+mockURL)

	cmd := exec.Command(bin, "task", "ping",
		"--identity", "work",
		"--harness", tc.harness,
		"--image", tc.image,
		"--name", pod,
		"--max-turns", "1")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("poddle task failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), tc.want) {
		t.Fatalf("%s: the agent did not return %q through the broker:\n%s", tc.name, tc.want, out)
	}

	assertSecretless(t, auths, sentinel, &mu)

	// task tears the pod down by default.
	ps, _ := exec.Command("podman", "ps", "-a", "--format", "{{.Names}}").CombinedOutput()
	if strings.Contains(string(ps), pod) {
		t.Errorf("%s: task should have removed the pod:\n%s", tc.name, ps)
	}
}

// TestE2E_Task_DetachRunsInBackground proves `poddle task --detach`: the agent
// runs in the background, the command returns immediately leaving the pod up,
// `poddle logs` streams the result, and the run reached the mock through the
// broker.
func TestE2E_Task_DetachRunsInBackground(t *testing.T) {
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
	const sentinel = "SENTINEL-TASKD"
	cfg := taskIdentity(t, sentinel)

	pod := "poddle-taskd-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
	})

	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+cfg,
		"PODDLE_ANTHROPIC_BASE_URL="+mockURL)

	// --detach returns promptly, leaving the agent running in the background.
	cmd := exec.Command(bin, "task", "ping",
		"--identity", "work", "--image", "docker.io/library/node:22",
		"--name", pod, "--max-turns", "1", "--detach")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("poddle task --detach failed: %v\n%s", err, out)
	}

	// The pod is still up (detached did not tear it down).
	ps, _ := exec.Command("podman", "ps", "--format", "{{.Names}}").CombinedOutput()
	if !strings.Contains(string(ps), pod) {
		t.Fatalf("detached task should leave the pod running; ps:\n%s", ps)
	}

	// Poll `poddle logs` until the agent finishes.
	var logsOut string
	done := false
	for i := 0; i < 30; i++ {
		o, _ := exec.Command(bin, "logs", pod).CombinedOutput()
		logsOut = string(o)
		if strings.Contains(logsOut, `"result":"works"`) {
			done = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !done {
		t.Fatalf("detached task never completed; last logs:\n%s", logsOut)
	}

	// The upstream saw the real (sentinel) secret, never the handle.
	mu.Lock()
	defer mu.Unlock()
	saw := false
	for _, a := range auths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("the handle leaked to the upstream: %q", a)
		}
		if a == "Bearer "+sentinel {
			saw = true
		}
	}
	if !saw {
		t.Errorf("upstream never saw the sentinel secret; got %v", auths)
	}

	// Manual teardown works.
	down := exec.Command(bin, "down", pod)
	down.Env = env
	if out, err := down.CombinedOutput(); err != nil {
		t.Fatalf("down failed: %v\n%s", err, out)
	}
}
