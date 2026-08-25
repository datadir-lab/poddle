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
// POST handles the JSON-RPC handshake Codex runs eagerly at startup —
// initialize (200 + Mcp-Session-Id header + InitializeResult),
// notifications/initialized (202, no body — a JSON-RPC notification has no id
// and gets no response), and tools/list (one "echo" tool). GET opens the
// server-initiated SSE stream and — per the Task 1 spike's caveat — HOLDS IT
// OPEN (blocks on the request context) rather than returning after one event,
// or Codex's MCP client reconnects in a tight loop. Records every request's
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
				// Anything else Codex might send (e.g. a ping) — a harmless
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
			// Hold the stream open — returning here makes Codex's MCP client
			// reconnect in a loop (Task 1 spike §5), noisy but not itself a
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

// TestE2E_MCP_Brokered proves Codex reaches a remote MCP server through the
// broker secretlessly: `poddle up` wires a connector-mcp connection into
// Codex's config (MCPWiring -> `codex mcp add`), Codex initializes it eagerly
// at startup (before the first model turn, per the Task 1 spike), and the
// broker swaps the pod's handle for the real (sentinel) bearer token on the
// wire — the mock MCP server never sees the handle. The model turn itself
// (mocked via mockOpenAIUp) may or may not complete cleanly; that is not what
// this test is proving, so a non-zero `poddle up` exit is not fatal here as
// long as the MCP handshake happened.
func TestE2E_MCP_Brokered(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	var mcpMu sync.Mutex
	var mcpAuths []string
	mcpMock := mockMCPServer(t, &mcpAuths, &mcpMu)
	mcpURL := brokerURL(t, mcpMock) + "/mcp"

	var oaMu sync.Mutex
	var oaAuths []string
	openaiMock := mockOpenAIUp(t, &oaAuths, &oaMu)
	openaiURL := brokerURL(t, openaiMock)

	const mcpSentinel = "SENTINEL-MCP-TOKEN"
	const oaSentinel = "SENTINEL-OPENAI"

	xdg := t.TempDir()
	writeOpenAIIdentity(t, xdg, oaSentinel) // identity "work" -> codex's provider + Provisions run

	// The mcp connection on disk, like runConnCase but connector = "mcp", no user
	// (bearer-only; see connector.go's "mcp" definition).
	connDir := filepath.Join(xdg, "poddle", "connections", "mcpsrv")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"mcp\"\nbase_url = \""+mcpURL+"\"\nowner = \"local\"\n")
	writeFile(t, filepath.Join(connDir, "mcp-token"), mcpSentinel)

	proj := t.TempDir()
	// harness = "codex" so Provisions installs the Codex CLI without needing an
	// explicit --harness flag (the default harness is claude-code).
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\nharness = \"codex\"\nconnectors = [\"mcpsrv\"]\n")

	pod := "poddle-mcp"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run() // fresh broker per test (its state dir is this test's temp)
	})

	// codex exec + the exact broker-provider -c overrides the "openai" case in
	// secretless_up_test.go uses (codexProviderFlags's shape). The MCP server
	// registration itself is NOT on this command line — MCPWiring already ran
	// it (`codex mcp add`) as a Setup step wired by the "mcpsrv" connector, so
	// Codex's config.toml already has the [mcp_servers.mcpsrv] entry before
	// this ever runs. The prompt is trivial — Codex tool-lists every configured
	// MCP server eagerly at startup, before the first model turn, so the MCP
	// handshake happens (and is provable) even if the model turn itself fails.
	inPod := "cd /workspace && codex exec --skip-git-repo-check " +
		`-c 'model_provider="poddle"' ` +
		`-c 'model_providers.poddle.name="poddle"' ` +
		`-c model_providers.poddle.base_url="\"$PODDLE_CODEX_BASE_URL\"" ` +
		`-c 'model_providers.poddle.env_key="OPENAI_API_KEY"' ` +
		`-c 'model_providers.poddle.wire_api="responses"' ` +
		"'say hi and stop'"

	cmd := exec.Command(bin, "up", pod, "--identity", "work", "--exec", inPod)
	cmd.Dir = proj
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		"PODDLE_OPENAI_BASE_URL="+openaiURL,
	)
	out, err := cmd.CombinedOutput()
	// A non-zero exit isn't fatal on its own: the model turn is a scripted,
	// minimal mock and may not satisfy Codex's full conversational loop. The
	// MCP handshake happens before any of that, at startup, so it is the
	// decisive assertion below — but surface `err` in every failure message.
	_ = err

	mcpMu.Lock()
	defer mcpMu.Unlock()
	if len(mcpAuths) == 0 {
		t.Fatalf("MCP server received no requests — codex did not reach it through the broker (up err: %v):\n%s", err, out)
	}
	sawSentinel := false
	for _, a := range mcpAuths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("the handle leaked to the MCP server: %q", a)
		}
		if a == "Bearer "+mcpSentinel {
			sawSentinel = true
		}
	}
	if !sawSentinel {
		t.Errorf("MCP server never saw the real token; got %v (up err: %v)\n%s", mcpAuths, err, out)
	}
}
