//go:build e2e

package e2e

import (
	"encoding/json"
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

// mcpToolName / mcpServerName identify the single tool the mock MCP server
// advertises and the server itself (JSON-RPC serverInfo.name) — no behavior
// depends on these beyond being present in tools/list and initialize.
const (
	mcpToolName   = "echo"
	mcpServerName = "poddle-mcp-mock"
)

// jsonrpcIn is the subset of a JSON-RPC 2.0 request the mock needs: the method
// to dispatch on, and the id (kept as raw JSON so it can be echoed back
// byte-for-byte — notifications, per spec, omit it entirely).
type jsonrpcIn struct {
	Method string          `json:"method"`
	ID     json.RawMessage `json:"id,omitempty"`
}

// mcpWriteResult writes a single-object JSON-RPC 2.0 result response (the
// Streamable HTTP spec allows a single JSON object, not just SSE, for a POST
// that isn't itself opening a stream).
func mcpWriteResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

// mockMCPServer is a minimal Streamable-HTTP MCP server on a single /mcp path:
// POST handles the JSON-RPC handshake — initialize (200 + Mcp-Session-Id
// header + InitializeResult), notifications/initialized (202, no body — a
// JSON-RPC notification has no id and gets no response), and tools/list (one
// "echo" tool). Codex runs this handshake eagerly at startup, before its first
// model turn (Task 1 spike); TestE2E_MCP_Brokered's per-agent rows are what
// check whether claude-code and opencode do the same. GET opens the
// server-initiated SSE stream and — per the Task 1 spike's caveat — HOLDS IT
// OPEN (blocks on the request context) rather than returning after one event,
// or a reconnecting MCP client loops tightly. Records every request's
// Authorization header (guarded by mu) — the secretless proof. Binds 0.0.0.0
// so the broker container reaches it at host.containers.internal (brokerURL).
func mockMCPServer(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		mu.Unlock()

		switch r.Method {
		case http.MethodPost:
			var in jsonrpcIn
			_ = json.NewDecoder(r.Body).Decode(&in)
			switch in.Method {
			case "initialize":
				w.Header().Set("Mcp-Session-Id", "poddle-mcp-mock-session")
				mcpWriteResult(w, in.ID, map[string]any{
					"protocolVersion": "2025-06-18",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": mcpServerName, "version": "0"},
				})
			case "notifications/initialized":
				// A notification carries no id and gets no response body — 202
				// per the Streamable HTTP spec.
				w.WriteHeader(http.StatusAccepted)
			case "tools/list":
				mcpWriteResult(w, in.ID, map[string]any{
					"tools": []any{
						map[string]any{
							"name":        mcpToolName,
							"description": "echo",
							"inputSchema": map[string]any{"type": "object"},
						},
					},
				})
			default:
				// Anything else a client might send (e.g. a ping) — a harmless
				// empty result for a request, silent 202 for a notification —
				// so the client doesn't stall waiting on an unhandled method.
				if len(in.ID) > 0 {
					mcpWriteResult(w, in.ID, map[string]any{})
				} else {
					w.WriteHeader(http.StatusAccepted)
				}
			}
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			fmt.Fprint(w, ": keepalive\n\n")
			if fl != nil {
				fl.Flush()
			}
			// Hold the stream open — returning here makes a reconnecting MCP
			// client loop (Task 1 spike §5), noisy but not itself a
			// gateway/broker problem. The connection tears down when the pod
			// (and its broker) are removed in t.Cleanup.
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind 0.0.0.0: %v", err)
	}
	srv := httptest.NewUnstartedServer(mux)
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// mockOpenAIStreamUp is a minimal SSE-streaming chat-completions mock (opencode
// always sends stream:true; a flat-JSON mock makes its SDK loop forever) that
// replies "works" — kept for mcp_test's existing callers, which don't need a
// distinctive marker. Delegates to mockOpenAIStreamUpMarker.
func mockOpenAIStreamUp(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return mockOpenAIStreamUpMarker(t, auths, mu, "works")
}

// mockOpenAIStreamUpMarker is mockOpenAIStreamUp parameterized on the streamed
// assistant content, so callers that need a distinctive success marker (e.g.
// secretless_up_test.go's opencode/pi rows) can tell a real reply apart from
// the harness merely echoing the prompt. Records auth; binds 0.0.0.0 so the
// broker container reaches it via host.containers.internal.
func mockOpenAIStreamUpMarker(t *testing.T, auths *[]string, mu *sync.Mutex, marker string) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, piChunk(map[string]any{"role": "assistant", "content": ""}, nil))
		fmt.Fprint(w, piChunk(map[string]any{"content": marker}, nil))
		fmt.Fprint(w, piChunk(map[string]any{}, "stop"))
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl, ok := w.(http.Flusher); ok {
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

// mcpSentinel is the bearer token the mock MCP server expects. It is the SAME
// value in every mcpCase row below — the connections/mcpsrv connection
// (connector=mcp, base_url=<mock>/mcp, mcp-token=mcpSentinel) is written
// identically into each row's own XDG dir by runMCPCase, so "shared" here
// means the same shape and the same constant, not one literal file reused
// across pods. It is deliberately distinct from every row's own model
// sentinel so the MCP assertion can't be satisfied by the model token
// leaking onto the wrong wire.
const mcpSentinel = "SENTINEL-MCP-TOKEN"

// mcpCase is one coding agent the brokered-MCP e2e drives through a real
// `poddle up --exec`: its harness/image, the identity writer + provider
// base-URL env var that points its OWN model traffic at a scripted mock
// (never the MCP mock — that one is wired identically for every row, see
// runMCPCase), and the in-pod invocation that starts it. inPod reuses each
// agent's verified e2e invocation shape from edit_test.go / secretless_up_test.go
// — only the prompt is trivialized here, since the point of this test is MCP
// initialization, not a full model turn.
type mcpCase struct {
	name          string                                                               // subtest name
	harness       string                                                               // .poddle.toml harness value
	image         string                                                               // .poddle.toml / pod image
	writeIdentity func(t *testing.T, xdg, sentinel string)                             // writeOpenAIIdentity / writeAnthropicIdentity (edit_test.go)
	upstreamEnv   string                                                               // provider base-URL env var poddle up reads (PODDLE_OPENAI_BASE_URL / PODDLE_ANTHROPIC_BASE_URL)
	modelMock     func(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server // this row's model-shaped mock upstream
	inPod         string                                                               // in-pod agent invocation run via `poddle up --exec`
}

var mcpCases = []mcpCase{
	{
		name:          "codex",
		harness:       "codex",
		image:         "docker.io/library/node:22",
		writeIdentity: writeOpenAIIdentity,
		upstreamEnv:   "PODDLE_OPENAI_BASE_URL",
		modelMock:     mockOpenAIUp,
		// codex exec + the exact broker-provider -c overrides the "openai" case in
		// secretless_up_test.go uses (codexProviderFlags's shape). The MCP server
		// registration itself is NOT on this command line — MCPWiring already ran
		// it (`codex mcp add`) as a Setup step wired by the "mcpsrv" connector, so
		// Codex's config.toml already has the [mcp_servers.mcpsrv] entry before
		// this ever runs. The prompt is trivial — Codex tool-lists every configured
		// MCP server eagerly at startup, before the first model turn, so the MCP
		// handshake happens (and is provable) even if the model turn itself fails.
		inPod: "cd /workspace && codex exec --skip-git-repo-check " +
			`-c 'model_provider="poddle"' ` +
			`-c 'model_providers.poddle.name="poddle"' ` +
			`-c model_providers.poddle.base_url="\"$PODDLE_CODEX_BASE_URL\"" ` +
			`-c 'model_providers.poddle.env_key="OPENAI_API_KEY"' ` +
			`-c 'model_providers.poddle.wire_api="responses"' ` +
			"'say hi and stop'",
	},
	{
		name:          "claude-code",
		harness:       "claude-code",
		image:         "docker.io/library/node:22",
		writeIdentity: writeAnthropicIdentity,
		upstreamEnv:   "PODDLE_ANTHROPIC_BASE_URL",
		// mockAnthropicUpOn (audit_test.go), not the loopback-only mockAnthropicUp
		// (secretless_up_test.go): the broker is a container and dials this mock at
		// host.containers.internal, which requires a 0.0.0.0 bind — every other e2e
		// test that needs the anthropic mock reachable from inside a container uses
		// mockAnthropicUpOn for exactly this reason.
		modelMock: func(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
			return mockAnthropicUpOn(t, "0.0.0.0:0", auths, mu)
		},
		// The verified non-interactive claude-code recipe (edit_test.go /
		// secretless_up_test.go): IS_SANDBOX + disabled non-essential traffic, a
		// pre-seeded onboarding marker (merged, not clobbered — see claudecode.go's
		// onboardingMerge), stdin from /dev/null (otherwise blocks). MCPWiring
		// (`claude mcp add`) already registered mcpsrv into ~/.claude.json as a Setup
		// step, before this ever runs. Whether claude-code tool-lists its MCP
		// servers eagerly like Codex, or lazily on first tool use, is exactly what
		// this row proves or disproves.
		inPod: "cd /workspace && export IS_SANDBOX=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1; " +
			`node -e 'const f=process.env.HOME+"/.claude.json",fs=require("fs");let c={};try{c=JSON.parse(fs.readFileSync(f,"utf8"))}catch(e){};c.hasCompletedOnboarding=true;fs.writeFileSync(f,JSON.stringify(c))'; ` +
			`claude -p "say hi and stop" --output-format json --max-turns 1 --dangerously-skip-permissions </dev/null`,
	},
	{
		name:          "opencode",
		harness:       "opencode",
		image:         "docker.io/library/node:22",
		writeIdentity: writeOpenAIIdentity,
		upstreamEnv:   "PODDLE_OPENAI_BASE_URL",
		// mockOpenAIStreamUp, not mockOpenAIChatUp: opencode always sends
		// stream:true, and mockOpenAIChatUp's flat non-streaming JSON (built for
		// aider --no-stream) makes opencode's SDK loop forever waiting on an SSE
		// response — the MCP handshake itself is unaffected either way.
		modelMock: mockOpenAIStreamUp,
		// opencode's verified headless invocation (edit_test.go): -m selects the
		// poddle/poddle-model provider Provisions wrote into the OPENCODE_CONFIG
		// layer; --auto auto-approves tool permissions so a trivial prompt never
		// stalls on a permission prompt. MCPWiring merged the mcpsrv remote-MCP
		// entry into that same OPENCODE_CONFIG layer as a Setup step, before this
		// ever runs. Whether opencode initializes it eagerly like Codex, or lazily
		// on first tool use, is exactly what this row proves or disproves.
		inPod: "cd /workspace && opencode run 'say hi and stop' -m poddle/poddle-model --format json --auto",
	},
	{
		name:          "pi",
		harness:       "pi",
		image:         "docker.io/library/node:22",
		writeIdentity: writeOpenAIIdentity,
		upstreamEnv:   "PODDLE_OPENAI_BASE_URL",
		// pi always sends stream:true, so it needs the SSE model mock (same reason
		// as opencode). pi has no built-in MCP: Provisions installs the pi-mcp-adapter
		// extension and MCPWiring merges the mcpsrv remote entry into
		// $PI_CODING_AGENT_DIR/mcp.json as a Setup step, before this runs. The adapter
		// connects (initialize + tools-list) eagerly at startup, before the first
		// model turn, so the MCP handshake is provable even if the model turn fails.
		modelMock: mockOpenAIStreamUp,
		inPod:     "cd /workspace && pi --provider poddle --model poddle-model -p 'say hi and stop'",
	},
	{
		name:          "gemini",
		harness:       "gemini",
		image:         "docker.io/library/node:22",
		writeIdentity: writeGoogleIdentity,
		upstreamEnv:   "PODDLE_GOOGLE_BASE_URL",
		// gemini's own model leg (streamGenerateContent SSE) — reuse mockGeminiUp
		// from secretless_up_test.go. MCPWiring merged the mcpsrv httpUrl server into
		// ~/.gemini/settings.json as a Setup step (the handle rides the Authorization
		// header via ${env}, expanded at load). gemini-cli discovers MCP eagerly at
		// startup (spike-verified), before the first model turn, so the handshake
		// reaches the broker even if the model turn itself fails.
		modelMock: mockGeminiUp,
		inPod:     "cd /workspace && gemini -p 'say hi and stop' --output-format json --yolo </dev/null",
	},
}

// TestE2E_MCP_Brokered proves each agent poddle drives (codex, claude-code,
// opencode, pi, gemini) reaches a remote MCP server through the broker secretlessly:
// `poddle up` wires a connector-mcp connection into the agent's own config
// (MCPWiring), and the broker swaps the pod's handle for the real (sentinel)
// bearer token on the wire — the mock MCP server never sees the handle. Each
// row's model turn is driven by its own scripted, provider-shaped mock
// (mockOpenAIUp / mockAnthropicUpOn / mockOpenAIStreamUp) under its own
// sentinel, distinct from mcpSentinel, so the MCP assertion can't be
// satisfied by the model token. The model turn itself may or may not
// complete cleanly against so minimal a script — a non-zero `poddle up` exit
// is not fatal here — the decisive, per-row assertion is the MCP handshake.
// Codex is verified (Task 1 spike) to perform that handshake eagerly, before
// any model turn; whether claude-code and opencode do the same is unverified
// and exactly what their rows here check — a row that never reaches the MCP
// mock is a real finding (lazy MCP init), not a test bug.
func TestE2E_MCP_Brokered(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)
	for _, tc := range mcpCases {
		t.Run(tc.name, func(t *testing.T) { runMCPCase(t, bin, tc) })
	}
}

// runMCPCase drives one mcpCase end to end. Everything is per-row and
// isolated (a fresh MCP mock instance, a fresh model mock instance, a fresh
// XDG dir, a fresh project dir, a uniquely-named pod) so one row's traffic can
// never be mistaken for another's — the decisive assertion at the end reads
// only this row's own mcpAuths.
func runMCPCase(t *testing.T, bin string, tc mcpCase) {
	var mcpMu sync.Mutex
	var mcpAuths []string
	mcpMock := mockMCPServer(t, &mcpAuths, &mcpMu)
	mcpURL := brokerURL(t, mcpMock) + "/mcp"

	var modelMu sync.Mutex
	var modelAuths []string
	modelMock := tc.modelMock(t, &modelAuths, &modelMu)
	modelURL := brokerURL(t, modelMock)

	modelSentinel := "SENTINEL-MODEL-" + strings.ToUpper(tc.name)

	xdg := t.TempDir()
	tc.writeIdentity(t, xdg, modelSentinel) // identity "work" -> this agent's own provider + Provisions run

	// The mcp connection on disk, like runConnCase but connector = "mcp", no user
	// (bearer-only; see connector.go's "mcp" definition). Same shape in every row
	// — only the XDG dir (and so the pod that reads it) differs.
	connDir := filepath.Join(xdg, "poddle", "connections", "mcpsrv")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"mcp\"\nbase_url = \""+mcpURL+"\"\nowner = \"local\"\n")
	writeFile(t, filepath.Join(connDir, "mcp-token"), mcpSentinel)

	proj := t.TempDir()
	// harness = tc.harness so Provisions installs the right agent CLI without
	// needing an explicit --harness flag; connectors = ["mcpsrv"] is the same
	// template shape as the connection above, in every row.
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \""+tc.image+"\"\nharness = \""+tc.harness+"\"\nconnectors = [\"mcpsrv\"]\n")

	pod := "poddle-mcp-" + tc.name
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run() // fresh broker per case (its state dir is this case's temp)
	})

	cmd := exec.Command(bin, "up", pod, "--identity", "work", "--exec", tc.inPod)
	cmd.Dir = proj
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		tc.upstreamEnv+"="+modelURL,
	)
	out, err := cmd.CombinedOutput()
	// A non-zero exit isn't fatal on its own: the model turn is a scripted,
	// minimal mock and may not satisfy this agent's full conversational loop.
	// If MCP initializes eagerly (as Codex does), the handshake happens before
	// any of that, so it is the decisive assertion below — but surface `err`
	// in every failure message.
	_ = err

	mcpMu.Lock()
	defer mcpMu.Unlock()
	if len(mcpAuths) == 0 {
		t.Fatalf("%s: MCP server received no requests — it did not reach it through the broker (up err: %v):\n%s", tc.name, err, out)
	}
	sawSentinel := false
	for _, a := range mcpAuths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("%s: the handle leaked to the MCP server: %q", tc.name, a)
		}
		if a == "Bearer "+mcpSentinel {
			sawSentinel = true
		}
	}
	if !sawSentinel {
		t.Errorf("%s: MCP server never saw the real token; got %v (up err: %v)\n%s", tc.name, mcpAuths, err, out)
	}
}
