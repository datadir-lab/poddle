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
	"time"
)

// oauthMaterialJSON mirrors the on-disk JSON shape of connector.OAuthMaterial
// (src/internal/connector/connection.go). The tests/e2e package lives outside
// src/, so Go's internal-package rule forbids importing the connector package
// directly — we reproduce its json tags here instead. Only the fields the broker
// needs to inject + refresh an MCP access token are set (client_secret / scope
// stay zero, exactly as the brief's OAuth material does). These tags MUST stay in
// sync with OAuthMaterial; a drift would make LoadOAuth read zero-value fields and
// the e2e would fail loudly (no token injected).
type oauthMaterialJSON struct {
	AccessToken   string    `json:"access"`
	RefreshToken  string    `json:"refresh"`
	TokenEndpoint string    `json:"token_endpoint"`
	ClientID      string    `json:"client_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	// RotatedAt is the newest-wins freshness stamp Task 6's write-back reconcile
	// compares (reconcileOAuth in cli/up/command.go). Zero-valued in the two
	// original cases above (they never assert on it); the write-back subtest
	// seeds and re-reads it explicitly.
	RotatedAt time.Time `json:"rotated_at"`
}

// The OAuth-MCP e2e's sentinels — all distinct from one another AND from the
// "poddle_" handle prefix, so no assertion can be satisfied by the wrong value
// leaking onto the wrong wire:
//   - oauthAccessSentinel:    the valid (unexpired) access token seeded in oauth.json.
//   - oauthStaleSentinel:     the expired access token seeded in oauth.json (must never reach the MCP server).
//   - oauthRefreshedSentinel: the access token the mock AS mints on refresh (what the MCP server must see post-refresh).
//   - oauthRefreshSentinel:   the refresh token seeded in oauth.json (POSTed to the AS's /token).
//   - oauthModelSentinel:     the openai identity secret (drives the codex model leg — a DIFFERENT upstream than the MCP one).
const (
	oauthAccessSentinel    = "SENTINEL-OAUTH-ACCESS"
	oauthStaleSentinel     = "SENTINEL-OAUTH-STALE"
	oauthRefreshedSentinel = "SENTINEL-OAUTH-REFRESHED"
	oauthRefreshSentinel   = "SENTINEL-OAUTH-REFRESHTOKEN"
	oauthModelSentinel     = "SENTINEL-OAUTH-MODEL"
)

// Write-back subtest sentinels (runOAuthMCPWriteBack): the pre-refresh expired
// access token, the pre-rotation refresh token r1 seeded on disk, the rotated
// refresh token r2 the mock AS mints, and the fresh access token that rides
// along with it. All four are distinct from each other AND from every other
// sentinel in this file, so the on-disk-after-second-`up` assertion can only
// pass if r2 (and nothing else) actually made the mirror -> reconcile ->
// oauth.json round trip.
const (
	oauthWBExpiredAccessSentinel   = "SENTINEL-OAUTH-WB-EXPIRED-ACCESS"
	oauthWBRefreshedAccessSentinel = "SENTINEL-OAUTH-WB-REFRESHED-ACCESS"
	oauthWBRefreshR1Sentinel       = "SENTINEL-OAUTH-WB-REFRESH-R1"
	oauthWBRefreshR2Sentinel       = "SENTINEL-OAUTH-WB-REFRESH-R2"
	oauthWBModelSentinel           = "SENTINEL-OAUTH-WB-MODEL"
)

// Reactive-retry subtest sentinels (runOAuthMCPReactiveRetry): the valid,
// not-yet-expired access token seeded on disk (the one the mock MCP server
// 401s once), the access token the mock AS mints on the broker's FORCED
// (reactive) refresh, and the seeded refresh token — asserted to never reach
// the MCP server on either the rejected or the retried request.
const (
	oauthRRValidAccessSentinel     = "SENTINEL-OAUTH-RR-VALID-ACCESS"
	oauthRRRefreshedAccessSentinel = "SENTINEL-OAUTH-RR-REFRESHED-ACCESS"
	oauthRRRefreshSentinel         = "SENTINEL-OAUTH-RR-REFRESHTOKEN"
	oauthRRModelSentinel           = "SENTINEL-OAUTH-RR-MODEL"
)

// mockOAuthAS is a minimal 0.0.0.0-bound mock OAuth 2.1 authorization server:
// a single /token endpoint that, on grant_type=refresh_token, mints newAccess
// (as `{"access_token":...,"token_type":"bearer","expires_in":3600}` — the
// shape src/internal/oauth's tokenResp decodes) and records that it was called
// (guarded by mu). We seed oauth.json directly on disk (the authorize / DCR /
// metadata legs are unit-tested in the earlier tasks), so the RUNTIME leg the
// broker's gateway drives — the refresh grant it performs when an access token
// is within refreshSkew of ExpiresAt — is the only endpoint this needs. Binds
// 0.0.0.0 so the containerized broker reaches it at host.containers.internal
// (brokerURL), exactly like every other e2e mock the broker dials.
func mockOAuthAS(t *testing.T, newAccess string, calls *int, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/token") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "refresh_token" {
			// The gateway only ever performs the refresh grant here; anything else
			// is a test-wiring bug, so fail it loudly rather than mint a token.
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		*calls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"` + newAccess + `","token_type":"bearer","expires_in":3600}`))
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

// mockOAuthASRotating is mockOAuthAS's write-back sibling: same single /token
// refresh-grant endpoint, but its 200 ALSO carries a `refresh_token` field
// (newRefresh) alongside the new access token. That is the one detail that
// makes the gateway's rotation actually mirror to disk — src/internal/oauth's
// Refresh keeps the OLD refresh token when the response omits refresh_token
// (a non-rotating provider), so persistRotation's
// `updated.RefreshToken == old.RefreshToken` guard would skip the write-back
// entirely if this mock behaved like mockOAuthAS. Records the grant's own
// refresh_token form value (the pre-rotation token the pod's oauth.json was
// seeded with) so the write-back test can assert the AS actually saw r1, not
// just that the AS was called.
func mockOAuthASRotating(t *testing.T, newAccess, newRefresh string, calls *int, gotRefreshToken *string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/token") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "refresh_token" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		*calls++
		*gotRefreshToken = r.PostForm.Get("refresh_token")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"` + newAccess + `","refresh_token":"` + newRefresh + `","token_type":"bearer","expires_in":3600}`))
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

// mockMCPServerReactive401 is mockMCPServer's reactive-retry sibling: it
// speaks the exact same Streamable-HTTP MCP handshake (initialize,
// notifications/initialized, tools/list — see jsonrpcIn/mcpWriteResult in
// mcp_test.go), but the FIRST POST it ever receives — codex's eager
// `initialize` call, carrying the still-valid access token the gateway
// injected verbatim (refreshIfStale's proactive path never touches a token
// that isn't stale) — is answered with a bare 401, simulating an upstream
// that revoked the token early. That is exactly the case the gateway's
// reactive path (forceRefresh, gateway.go's ModifyResponse) exists for: it
// force-refreshes and replays the SAME request once, transparently to the
// pod. Every POST after the first behaves like mockMCPServer. Records every
// request's Authorization (guarded by mu) — the decisive proof is mcpAuths[0]
// (the rejected, pre-refresh bearer) vs mcpAuths[1] (the replay's refreshed
// bearer, which is what the pod's `initialize` call actually resolves to).
func mockMCPServerReactive401(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	var postCount int
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		mu.Unlock()

		switch r.Method {
		case http.MethodPost:
			mu.Lock()
			postCount++
			n := postCount
			mu.Unlock()
			if n == 1 {
				// An upstream 401 with a WWW-Authenticate the pod must NEVER see —
				// the gateway strips it unconditionally for an OAuth credential
				// (gateway.go's ModifyResponse) before this retry-or-surface logic
				// even runs, whether the retry succeeds or not.
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
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

// oauthMCPCase is one runtime scenario the OAuth-MCP e2e drives through a real
// `poddle up --exec`: the seeded oauth.json's access token + expiry, what the
// MCP server must (and must not) see on the wire, and whether the broker is
// expected to refresh against the mock AS.
type oauthMCPCase struct {
	name         string        // subtest name
	ttl          time.Duration // added to time.Now() for oauth.json's ExpiresAt (negative = already expired)
	accessToken  string        // access token seeded in oauth.json
	wantBearer   string        // the Authorization the MCP server MUST see (proves the broker injected the OAuth token)
	forbidBearer string        // an Authorization the MCP server must NEVER see ("" = skip; used to prove the stale token never egresses)
	wantRefresh  bool          // whether the mock AS /token MUST have been called (true only for the expired case)
}

var oauthMCPCases = []oauthMCPCase{
	{
		// Valid, unexpired access token: the broker injects it verbatim and never
		// touches the AS. ExpiresAt is far beyond refreshSkew (60s), so
		// refreshIfStale returns the credential untouched.
		name:        "valid",
		ttl:         time.Hour,
		accessToken: oauthAccessSentinel,
		wantBearer:  "Bearer " + oauthAccessSentinel,
		wantRefresh: false,
	},
	{
		// Already-expired access token: refreshIfStale POSTs grant_type=refresh_token
		// to the AS, swaps in the refreshed token, and injects THAT — the stale one
		// must never reach the MCP server.
		name:         "expired-refreshes",
		ttl:          -time.Hour,
		accessToken:  oauthStaleSentinel,
		wantBearer:   "Bearer " + oauthRefreshedSentinel,
		forbidBearer: "Bearer " + oauthStaleSentinel,
		wantRefresh:  true,
	},
}

// TestE2E_OAuthMCP proves a pod reaches an OAuth-protected MCP server through the
// broker SECRETLESSLY, including token refresh, against real podman. It reuses the
// brokered-MCP e2e's codex harness (mcpCases' "codex" row: writeOpenAIIdentity +
// the mockOpenAIUp model mock + the exact `codex exec` -c overrides) so the only
// new surface is the OAuth credential leg: instead of a static mcp-token, the
// connection carries an oauth.json (seeded directly on disk — the connect-add
// browser flow is unit-tested in Task 5 and can't be injected into the poddle
// BINARY driven here). When oauth.json is present, connector.Credential builds a
// ModeOAuthBearer credential from it (ignoring any static token file), and the
// gateway injects — and, for a stale token, refreshes — the OAuth access token on
// the wire. The pod only ever holds the `poddle_` handle. Two isolated subtests:
// a valid token (no refresh) and an already-expired token (broker refreshes
// end-to-end against a mock AS). NOTE: the podman run is CI-only; locally this
// skips (requirePodman) — it is written + compiled here, exercised on GitHub CI.
func TestE2E_OAuthMCP(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	// Reuse the codex row's verified in-pod invocation by name (robust against
	// reordering mcpCases) — its exact `codex exec` -c overrides + trivial prompt
	// perform the MCP handshake eagerly at startup, before any model turn.
	codexInPod := ""
	for _, c := range mcpCases {
		if c.name == "codex" {
			codexInPod = c.inPod
			break
		}
	}
	if codexInPod == "" {
		t.Fatal("codex mcpCase not found — its reuse target moved; update this test")
	}

	for _, tc := range oauthMCPCases {
		t.Run(tc.name, func(t *testing.T) { runOAuthMCPCase(t, bin, codexInPod, tc) })
	}

	// Task 9: the two behaviors this branch introduced beyond #141's baseline —
	// a rotated refresh token reconciled back to the host's oauth.json, and the
	// broker's reactive (post-401) refresh-and-retry. Both run as t.Run
	// subtests of TestE2E_OAuthMCP (not sibling top-level Test funcs) so the
	// e2e-oauth-mcp Taskfile target's `-run TestE2E_OAuthMCP` keeps covering
	// them without any CI-wiring change.
	t.Run("write-back", func(t *testing.T) { runOAuthMCPWriteBack(t, bin, codexInPod) })
	t.Run("reactive-retry", func(t *testing.T) { runOAuthMCPReactiveRetry(t, bin, codexInPod) })
}

// runOAuthMCPCase drives one oauthMCPCase end to end. Everything is per-row and
// isolated (a fresh MCP mock, a fresh mock AS, a fresh model mock, a fresh XDG
// dir + project dir, a uniquely-named pod + a fresh broker) so one row's traffic
// can never be mistaken for another's — the decisive assertions read only this
// row's own mcpAuths and AS call count.
func runOAuthMCPCase(t *testing.T, bin, codexInPod string, tc oauthMCPCase) {
	// The remote MCP server: records every request's Authorization (the secretless
	// proof) and speaks the MCP handshake codex runs eagerly at startup.
	var mcpMu sync.Mutex
	var mcpAuths []string
	mcpMock := mockMCPServer(t, &mcpAuths, &mcpMu)
	mcpURL := brokerURL(t, mcpMock) + "/mcp"

	// The mock authorization server: its /token refresh grant mints the refreshed
	// access token and counts its own calls.
	var asMu sync.Mutex
	var asCalls int
	asMock := mockOAuthAS(t, oauthRefreshedSentinel, &asCalls, &asMu)
	asURL := brokerURL(t, asMock)

	// The codex model leg — a DIFFERENT upstream, under its own sentinel, so the
	// MCP assertion can't be satisfied by the model token leaking onto the MCP wire.
	var modelMu sync.Mutex
	var modelAuths []string
	modelMock := mockOpenAIUp(t, &modelAuths, &modelMu)
	modelURL := brokerURL(t, modelMock)

	xdg := t.TempDir()
	writeOpenAIIdentity(t, xdg, oauthModelSentinel) // identity "work" -> codex's own provider + Provisions run

	// Seed the MCP connection on disk exactly as connect-add would leave it, but
	// WITHOUT running the browser flow: meta.toml (connector=mcp, base_url=<mock>,
	// no static token) + an oauth.json holding the OAuth material. connector.Credential
	// prefers oauth.json over any static mcp-token, so none is written.
	connDir := filepath.Join(xdg, "poddle", "connections", "oauthmcp")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"mcp\"\nbase_url = \""+mcpURL+"\"\nowner = \"local\"\n")
	oauthJSON, err := json.Marshal(oauthMaterialJSON{
		AccessToken:   tc.accessToken,
		RefreshToken:  oauthRefreshSentinel,
		TokenEndpoint: asURL + "/token",
		ClientID:      "cid",
		ExpiresAt:     time.Now().Add(tc.ttl),
	})
	if err != nil {
		t.Fatalf("marshal oauth material: %v", err)
	}
	writeFile(t, filepath.Join(connDir, "oauth.json"), string(oauthJSON)) // 0600 (writeFile)

	proj := t.TempDir()
	// harness=codex so Provisions installs the codex CLI; connectors=["oauthmcp"]
	// wires the connection above (MCPWiring runs `codex mcp add` pointing at the
	// broker gateway with the handle) — identical to the brokered-MCP e2e's codex row.
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\nharness = \"codex\"\nconnectors = [\"oauthmcp\"]\n")

	pod := "poddle-oauthmcp-" + tc.name
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run() // fresh broker per case (its state dir is this case's temp)
	})

	cmd := exec.Command(bin, "up", pod, "--identity", "work", "--exec", codexInPod)
	cmd.Dir = proj
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		"PODDLE_OPENAI_BASE_URL="+modelURL,
	)
	out, err := cmd.CombinedOutput()
	// A non-zero exit isn't fatal on its own: the model turn is a minimal scripted
	// mock and may not satisfy codex's full loop. The MCP handshake happens eagerly
	// at startup (before any model turn), so it is the decisive assertion below —
	// but surface `err` in every failure message.
	_ = err

	mcpMu.Lock()
	defer mcpMu.Unlock()
	if len(mcpAuths) == 0 {
		t.Fatalf("%s: MCP server received no requests — the pod did not reach it through the broker (up err: %v):\n%s", tc.name, err, out)
	}
	sawWanted := false
	for _, a := range mcpAuths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("%s: the handle leaked to the MCP server: %q", tc.name, a)
		}
		if tc.forbidBearer != "" && a == tc.forbidBearer {
			t.Errorf("%s: the MCP server saw a forbidden token %q (broker did not refresh/swap it)", tc.name, a)
		}
		if a == tc.wantBearer {
			sawWanted = true
		}
	}
	if !sawWanted {
		t.Errorf("%s: MCP server never saw the injected OAuth token %q; got %v (up err: %v)\n%s", tc.name, tc.wantBearer, mcpAuths, err, out)
	}

	asMu.Lock()
	gotRefresh := asCalls
	asMu.Unlock()
	if tc.wantRefresh && gotRefresh == 0 {
		t.Errorf("%s: expected the broker to refresh against the AS, but /token was never called", tc.name)
	}
	if !tc.wantRefresh && gotRefresh != 0 {
		t.Errorf("%s: the broker refreshed a still-valid token (%d AS /token calls; expected 0)", tc.name, gotRefresh)
	}
}

// runOAuthMCPWriteBack proves Task 6's write-back end to end: a refresh token
// the gateway rotates while a pod runs is durably mirrored to poddled's state
// dir, and a SECOND `poddle up` — which drains that mirror
// (b.OAuthMirror()/GET /oauth/mirror) and reconciles it (loadConnectorOAuth/
// reconcileOAuth in cli/up/command.go) BEFORE it ever creates a pod — writes
// the rotated material back to connections/<conn>/oauth.json on the host.
// This is the full gateway-persist -> host-drain -> oauth.json round trip, not
// just the in-memory mirror: the decisive assertion re-reads oauth.json from
// disk after the second `up`, not any daemon/client API.
//
// Sequence:
//  1. Seed oauth.json with an EXPIRED access token, refresh token r1, and an
//     OLD RotatedAt (24h in the past — unambiguously older than whatever the
//     gateway stamps on rotation).
//  2. First `up --exec <codex>`: codex's eager MCP initialize hits the
//     gateway, which finds the access token expired, refreshes at the mock
//     AS (rotating r1 -> r2 — see mockOAuthASRotating; a non-rotating AS
//     response would make persistRotation skip the mirror write entirely),
//     injects the refreshed access token, and mirrors {access, r2, RotatedAt}
//     to poddled's oauth-mirror dir.
//  3. Second `up --exec true` (SAME xdg, so the SAME connections dir and the
//     SAME running poddled/broker): buildSpec drains the mirror before doing
//     anything pod-related, sees the mirror's RotatedAt is newer than the
//     on-disk r1's, adopts it, and writes it back to oauth.json.
//  4. Read connections/oauthwb/oauth.json off disk and assert it now holds
//     r2 and a RotatedAt strictly after the seeded OLD one.
func runOAuthMCPWriteBack(t *testing.T, bin, codexInPod string) {
	var mcpMu sync.Mutex
	var mcpAuths []string
	mcpMock := mockMCPServer(t, &mcpAuths, &mcpMu)
	mcpURL := brokerURL(t, mcpMock) + "/mcp"

	var asMu sync.Mutex
	var asCalls int
	var asGotRefreshToken string
	asMock := mockOAuthASRotating(t, oauthWBRefreshedAccessSentinel, oauthWBRefreshR2Sentinel, &asCalls, &asGotRefreshToken, &asMu)
	asURL := brokerURL(t, asMock)

	var modelMu sync.Mutex
	var modelAuths []string
	modelMock := mockOpenAIUp(t, &modelAuths, &modelMu)
	modelURL := brokerURL(t, modelMock)

	xdg := t.TempDir() // reused for BOTH `up` calls below — this IS the reconcile's shared state
	writeOpenAIIdentity(t, xdg, oauthWBModelSentinel)

	connDir := filepath.Join(xdg, "poddle", "connections", "oauthwb")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"mcp\"\nbase_url = \""+mcpURL+"\"\nowner = \"local\"\n")
	oldRotatedAt := time.Now().Add(-24 * time.Hour)
	oauthJSON, err := json.Marshal(oauthMaterialJSON{
		AccessToken:   oauthWBExpiredAccessSentinel,
		RefreshToken:  oauthWBRefreshR1Sentinel,
		TokenEndpoint: asURL + "/token",
		ClientID:      "cid",
		ExpiresAt:     time.Now().Add(-time.Hour), // already expired -> proactive refresh on the first request
		RotatedAt:     oldRotatedAt,
	})
	if err != nil {
		t.Fatalf("marshal oauth material: %v", err)
	}
	oauthPath := filepath.Join(connDir, "oauth.json")
	writeFile(t, oauthPath, string(oauthJSON)) // 0600 (writeFile)

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\nharness = \"codex\"\nconnectors = [\"oauthwb\"]\n")

	pod1 := "poddle-oauthmcp-writeback"
	pod2 := "poddle-oauthmcp-writeback-drain"
	_ = exec.Command("podman", "rm", "-f", pod1).Run()
	_ = exec.Command("podman", "rm", "-f", pod2).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod1).Run()
		_ = exec.Command("podman", "rm", "-f", pod2).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
	})

	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		"PODDLE_OPENAI_BASE_URL="+modelURL,
	)

	// Step 1: the real MCP handshake — this is what rotates r1 -> r2 at the
	// mock AS and mirrors it to poddled's state dir.
	cmd1 := exec.Command(bin, "up", pod1, "--identity", "work", "--exec", codexInPod)
	cmd1.Dir = proj
	cmd1.Env = env
	out1, err1 := cmd1.CombinedOutput()
	_ = err1 // non-fatal on its own — see runOAuthMCPCase's identical comment

	mcpMu.Lock()
	if len(mcpAuths) == 0 {
		mcpMu.Unlock()
		t.Fatalf("write-back: MCP server received no requests — the pod did not reach it through the broker (up err: %v):\n%s", err1, out1)
	}
	wantBearer := "Bearer " + oauthWBRefreshedAccessSentinel
	forbidBearer := "Bearer " + oauthWBExpiredAccessSentinel
	sawWanted := false
	for _, a := range mcpAuths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("write-back: the handle leaked to the MCP server: %q", a)
		}
		if a == forbidBearer {
			t.Errorf("write-back: the MCP server saw the pre-refresh expired token %q", a)
		}
		if a == "Bearer "+oauthWBRefreshR1Sentinel || a == "Bearer "+oauthWBRefreshR2Sentinel {
			t.Errorf("write-back: a refresh token leaked to the MCP server as a bearer: %q", a)
		}
		if a == wantBearer {
			sawWanted = true
		}
	}
	mcpMu.Unlock()
	if !sawWanted {
		t.Errorf("write-back: MCP server never saw the refreshed OAuth token %q; got %v (up err: %v)\n%s", wantBearer, mcpAuths, err1, out1)
	}

	asMu.Lock()
	gotCalls := asCalls
	gotRefreshToken := asGotRefreshToken
	asMu.Unlock()
	if gotCalls == 0 {
		t.Fatalf("write-back: expected the broker to refresh against the AS, but /token was never called (up err: %v)\n%s", err1, out1)
	}
	if gotRefreshToken != oauthWBRefreshR1Sentinel {
		t.Errorf("write-back: the AS's refresh grant carried refresh_token %q, want the seeded r1 %q", gotRefreshToken, oauthWBRefreshR1Sentinel)
	}

	// Step 2: a second `up`, same xdg (same connections dir, same poddled) —
	// buildSpec drains the mirror and reconciles it into oauth.json before this
	// pod is ever created. A trivial --exec is enough: the reconcile runs
	// unconditionally for every connector on every `up`, regardless of what the
	// pod itself does (cli/up/command.go's buildSpec, connectors loop).
	cmd2 := exec.Command(bin, "up", pod2, "--identity", "work", "--exec", "true")
	cmd2.Dir = proj
	cmd2.Env = env
	out2, err2 := cmd2.CombinedOutput()
	// Not fatal on its own: the reconcile write happens inside buildSpec, BEFORE
	// the second `up` ever creates pod2 — so even a later, unrelated pod-creation
	// failure would not undo an already-completed write-back. Surface err2/out2
	// in the assertions below instead of failing fast on it.

	b, err := os.ReadFile(oauthPath)
	if err != nil {
		t.Fatalf("write-back: read back connections/oauthwb/oauth.json: %v (second up err: %v)\n%s", err, err2, out2)
	}
	var got oauthMaterialJSON
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("write-back: unmarshal reconciled oauth.json: %v\n%s", err, b)
	}
	if got.RefreshToken != oauthWBRefreshR2Sentinel {
		t.Errorf("write-back: oauth.json refresh token = %q after the second `up`, want the rotated r2 %q — write-back did not reach disk (second up err: %v)\n%s", got.RefreshToken, oauthWBRefreshR2Sentinel, err2, b)
	}
	if got.AccessToken != oauthWBRefreshedAccessSentinel {
		t.Errorf("write-back: oauth.json access token = %q after the second `up`, want the refreshed access token %q\n%s", got.AccessToken, oauthWBRefreshedAccessSentinel, b)
	}
	if !got.RotatedAt.After(oldRotatedAt) {
		t.Errorf("write-back: oauth.json rotated_at = %v after the second `up`, want it strictly after the seeded OLD rotated_at %v\n%s", got.RotatedAt, oldRotatedAt, b)
	}
}

// runOAuthMCPReactiveRetry proves the gateway's reactive (post-401) refresh
// path (Gateway.forceRefresh, called from ServeHTTP's proxy.ModifyResponse)
// end to end: an access token that is NOT yet stale by expiry (so
// refreshIfStale's proactive check never fires) can still be rejected early
// by the upstream — mockMCPServerReactive401 401s exactly the first request —
// and the broker must force a refresh and transparently replay the SAME
// request once, so the pod's own MCP call ultimately succeeds without ever
// running its own OAuth handshake. The unit tests already prove the
// WWW-Authenticate header is stripped on every OAuth response (gateway_test.go);
// this e2e proves the observable end result — the retried request landing at
// the mock MCP server under the refreshed bearer, and the pod's handshake
// continuing past it.
func runOAuthMCPReactiveRetry(t *testing.T, bin, codexInPod string) {
	var mcpMu sync.Mutex
	var mcpAuths []string
	mcpMock := mockMCPServerReactive401(t, &mcpAuths, &mcpMu)
	mcpURL := brokerURL(t, mcpMock) + "/mcp"

	var asMu sync.Mutex
	var asCalls int
	asMock := mockOAuthAS(t, oauthRRRefreshedAccessSentinel, &asCalls, &asMu)
	asURL := brokerURL(t, asMock)

	var modelMu sync.Mutex
	var modelAuths []string
	modelMock := mockOpenAIUp(t, &modelAuths, &modelMu)
	modelURL := brokerURL(t, modelMock)

	xdg := t.TempDir()
	writeOpenAIIdentity(t, xdg, oauthRRModelSentinel)

	connDir := filepath.Join(xdg, "poddle", "connections", "oauthrr")
	writeFile(t, filepath.Join(connDir, "meta.toml"),
		"connector = \"mcp\"\nbase_url = \""+mcpURL+"\"\nowner = \"local\"\n")
	oauthJSON, err := json.Marshal(oauthMaterialJSON{
		AccessToken:   oauthRRValidAccessSentinel,
		RefreshToken:  oauthRRRefreshSentinel,
		TokenEndpoint: asURL + "/token",
		ClientID:      "cid",
		ExpiresAt:     time.Now().Add(time.Hour), // NOT stale — only the reactive (401) path should fire
	})
	if err != nil {
		t.Fatalf("marshal oauth material: %v", err)
	}
	writeFile(t, filepath.Join(connDir, "oauth.json"), string(oauthJSON)) // 0600 (writeFile)

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\nharness = \"codex\"\nconnectors = [\"oauthrr\"]\n")

	pod := "poddle-oauthmcp-reactive-retry"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
	})

	cmd := exec.Command(bin, "up", pod, "--identity", "work", "--exec", codexInPod)
	cmd.Dir = proj
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		"PODDLE_OPENAI_BASE_URL="+modelURL,
	)
	out, err := cmd.CombinedOutput()
	_ = err // non-fatal on its own — see runOAuthMCPCase's identical comment

	mcpMu.Lock()
	defer mcpMu.Unlock()
	if len(mcpAuths) < 2 {
		t.Fatalf("reactive-retry: expected at least 2 MCP requests (the 401'd original + the force-refreshed replay), got %d: %v (up err: %v)\n%s", len(mcpAuths), mcpAuths, err, out)
	}
	rejected := "Bearer " + oauthRRValidAccessSentinel
	refreshed := "Bearer " + oauthRRRefreshedAccessSentinel
	if mcpAuths[0] != rejected {
		t.Errorf("reactive-retry: first MCP request carried %q, want the seeded still-valid token %q", mcpAuths[0], rejected)
	}
	if mcpAuths[1] != refreshed {
		t.Errorf("reactive-retry: retried MCP request carried %q, want the force-refreshed token %q — the broker did not refresh+replay on the upstream 401", mcpAuths[1], refreshed)
	}
	for i, a := range mcpAuths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("reactive-retry: the handle leaked to the MCP server (request %d): %q", i, a)
		}
		if a == "Bearer "+oauthRRRefreshSentinel {
			t.Errorf("reactive-retry: the raw refresh token leaked to the MCP server as a bearer (request %d): %q", i, a)
		}
		if i >= 1 && a != refreshed {
			t.Errorf("reactive-retry: request %d (after the retry) carried %q, want every post-retry request on the refreshed token %q — the vault swap did not stick", i, a, refreshed)
		}
	}

	asMu.Lock()
	gotRefresh := asCalls
	asMu.Unlock()
	if gotRefresh == 0 {
		t.Errorf("reactive-retry: expected the broker to force-refresh against the AS after the upstream 401, but /token was never called")
	}
}
