//go:build e2e

package e2e

import (
	"encoding/json"
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
