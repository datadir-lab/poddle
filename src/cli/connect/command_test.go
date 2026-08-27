package connect

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/connector"
	"github.com/datadir-lab/poddle/src/internal/oauth"
)

func TestConnect_AddViaStdin(t *testing.T) {
	store := connector.NewStore(t.TempDir())
	c := NewCmd(&app.App{Connections: store})
	c.SetArgs([]string{"add", "my-forgejo", "--connector", "forgejo", "--url", "http://forge", "--user", "me"})
	c.SetIn(strings.NewReader("SECRET-TOKEN\n")) // token piped, never in argv

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, err := store.Get("my-forgejo")
	if err != nil {
		t.Fatalf("connection not stored: %v", err)
	}
	if got.Connector != "forgejo" || got.User != "me" || got.BaseURL != "http://forge" {
		t.Errorf("stored connection = %+v", got)
	}
}

func TestConnect_LsAndRm(t *testing.T) {
	store := connector.NewStore(t.TempDir())
	if _, err := store.Create("wp", "woodpecker", "http://wp", "", "TOK", ""); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	c := NewCmd(&app.App{Connections: store})
	c.SetOut(&out)
	c.SetArgs([]string{"ls"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"NAME", "wp", "woodpecker"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("ls output missing %q:\n%s", w, out.String())
		}
	}

	rm := NewCmd(&app.App{Connections: store})
	rm.SetArgs([]string{"rm", "wp"})
	if err := rm.Execute(); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.List(); len(list) != 0 {
		t.Errorf("expected removed, got %v", list)
	}
}

// newMockOAuthMCP wires a mock MCP resource server + mock authorization
// server together: the MCP 401s with a WWW-Authenticate pointer to its
// protected-resource metadata, which names the AS; the AS serves its own
// .well-known metadata, a DCR /register endpoint, an /authorize endpoint
// that auto-approves (302 straight to redirect_uri with a code), and a
// /token endpoint. Mirrors oauth.TestDiscover_ChainAndErrNoOAuth's shape.
func newMockOAuthMCP(t *testing.T) (mcpURL string, asURL string) {
	t.Helper()
	var mu sync.Mutex
	var wantChallenge string // code_challenge captured from /authorize, checked against /token's code_verifier

	var as *httptest.Server
	as = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": as.URL + "/authorize",
				"token_endpoint":         as.URL + "/token",
				"registration_endpoint":  as.URL + "/register",
			})
		case "/register":
			json.NewEncoder(w).Encode(map[string]string{
				"client_id":     "dyn-client",
				"client_secret": "dyn-secret",
			})
		case "/authorize":
			q := r.URL.Query()
			mu.Lock()
			wantChallenge = q.Get("code_challenge")
			mu.Unlock()
			dest := q.Get("redirect_uri") + "?" + url.Values{
				"code":  {"THECODE"},
				"state": {q.Get("state")},
			}.Encode()
			http.Redirect(w, r, dest, http.StatusFound)
		case "/token":
			_ = r.ParseForm()
			verifier := r.FormValue("code_verifier")
			mu.Lock()
			challenge := wantChallenge
			mu.Unlock()
			sum := sha256.Sum256([]byte(verifier))
			gotChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
			if r.FormValue("grant_type") != "authorization_code" || r.FormValue("code") != "THECODE" ||
				verifier == "" || challenge == "" || gotChallenge != challenge {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "ACCESS-XYZ-SECRET",
				"refresh_token": "REFRESH-XYZ-SECRET",
				"expires_in":    3600,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(as.Close)

	var mcp *httptest.Server
	mcp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{as.URL}})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+mcp.URL+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(mcp.Close)

	return mcp.URL, as.URL
}

// headlessOpen drives the OAuth authorize URL the same way a real browser
// would (an HTTP GET, following the AS's redirect back to the loopback
// callback) without ever popping a window — this is what lets the test
// exercise AuthCodeFlow end-to-end.
func headlessOpen(t *testing.T) func(string) error {
	t.Helper()
	return func(u string) error {
		go func() {
			resp, err := http.Get(u)
			if err != nil {
				t.Logf("headless open: %v", err)
				return
			}
			resp.Body.Close()
		}()
		return nil
	}
}

func TestConnect_AddOAuth_HeadlessBrowserFlow(t *testing.T) {
	mcpURL, _ := newMockOAuthMCP(t)

	orig := oauth.OpenBrowser
	oauth.OpenBrowser = headlessOpen(t)
	t.Cleanup(func() { oauth.OpenBrowser = orig })

	store := connector.NewStore(t.TempDir())
	var out bytes.Buffer
	c := NewCmd(&app.App{Connections: store})
	c.SetOut(&out)
	c.SetArgs([]string{"add", "my-mcp", "--connector", "mcp", "--url", mcpURL})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	mat, ok, err := store.LoadOAuth("my-mcp")
	if err != nil {
		t.Fatalf("LoadOAuth: %v", err)
	}
	if !ok {
		t.Fatal("expected oauth.json to be written, found none")
	}
	if mat.AccessToken != "ACCESS-XYZ-SECRET" {
		t.Errorf("AccessToken = %q", mat.AccessToken)
	}
	if mat.RefreshToken != "REFRESH-XYZ-SECRET" {
		t.Errorf("RefreshToken = %q", mat.RefreshToken)
	}
	if mat.TokenEndpoint == "" {
		t.Error("TokenEndpoint not saved")
	}
	if mat.ClientID != "dyn-client" {
		t.Errorf("ClientID = %q, want the DCR-issued dyn-client", mat.ClientID)
	}
	if mat.ExpiresAt.IsZero() {
		t.Error("ExpiresAt not set from expires_in")
	}
	if mat.RotatedAt.IsZero() {
		t.Error("RotatedAt not stamped when a fresh grant was sealed")
	}

	// Static-token path stays empty for an OAuth connection.
	conn, err := store.Get("my-mcp")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if conn.BaseURL != mcpURL {
		t.Errorf("BaseURL = %q", conn.BaseURL)
	}

	for _, secret := range []string{"ACCESS-XYZ-SECRET", "REFRESH-XYZ-SECRET"} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("stdout leaked a token:\n%s", out.String())
		}
	}
}

// newMockOAuthMCPDevice is like newMockOAuthMCP but the AS additionally
// advertises a device_authorization_endpoint (RFC 8628) and serves
// /device_authorization + a device-code-grant /token that reports
// authorization_pending once before issuing the real token — so the test
// exercises DeviceFlow's poll loop, not just its happy-path single request.
func newMockOAuthMCPDevice(t *testing.T) (mcpURL string) {
	t.Helper()
	var pendingLeft int32 = 1

	var as *httptest.Server
	as = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint":        as.URL + "/authorize",
				"token_endpoint":                as.URL + "/token",
				"registration_endpoint":         as.URL + "/register",
				"device_authorization_endpoint": as.URL + "/device_authorization",
			})
		case "/register":
			json.NewEncoder(w).Encode(map[string]string{
				"client_id":     "dyn-client",
				"client_secret": "dyn-secret",
			})
		case "/device_authorization":
			json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "DEVCODE123",
				"user_code":        "WDJB-MJHT",
				"verification_uri": as.URL + "/activate",
				"interval":         1,
				"expires_in":       300,
			})
		case "/token":
			_ = r.ParseForm()
			if r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.FormValue("device_code") != "DEVCODE123" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			if atomic.AddInt32(&pendingLeft, -1) >= 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "DEVICE-ACCESS-SECRET",
				"refresh_token": "DEVICE-REFRESH-SECRET",
				"expires_in":    3600,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(as.Close)

	var mcp *httptest.Server
	mcp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{as.URL}})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+mcp.URL+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(mcp.Close)

	return mcp.URL
}

// TestConnect_AddOAuth_DeviceFlow mirrors
// TestConnect_AddOAuth_HeadlessBrowserFlow but drives `connect add --device`:
// no browser/headlessOpen is involved at all — the mock AS's
// device_authorization_endpoint and device-code-grant /token stand in for a
// headless operator completing consent out-of-band.
func TestConnect_AddOAuth_DeviceFlow(t *testing.T) {
	mcpURL := newMockOAuthMCPDevice(t)

	store := connector.NewStore(t.TempDir())
	var out bytes.Buffer
	c := NewCmd(&app.App{Connections: store})
	c.SetOut(&out)
	c.SetArgs([]string{"add", "my-mcp", "--connector", "mcp", "--url", mcpURL, "--device"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	mat, ok, err := store.LoadOAuth("my-mcp")
	if err != nil {
		t.Fatalf("LoadOAuth: %v", err)
	}
	if !ok {
		t.Fatal("expected oauth.json to be written, found none")
	}
	if mat.AccessToken != "DEVICE-ACCESS-SECRET" {
		t.Errorf("AccessToken = %q", mat.AccessToken)
	}
	if mat.RefreshToken != "DEVICE-REFRESH-SECRET" {
		t.Errorf("RefreshToken = %q", mat.RefreshToken)
	}
	if mat.TokenEndpoint == "" {
		t.Error("TokenEndpoint not saved")
	}
	if mat.ClientID != "dyn-client" {
		t.Errorf("ClientID = %q, want the DCR-issued dyn-client", mat.ClientID)
	}
	if mat.ExpiresAt.IsZero() {
		t.Error("ExpiresAt not set from expires_in")
	}
	if mat.RotatedAt.IsZero() {
		t.Error("RotatedAt not stamped when a fresh grant was sealed")
	}

	for _, want := range []string{"my-mcp", "WDJB-MJHT"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(out.String(), "/activate") {
		t.Errorf("output missing the verification URI:\n%s", out.String())
	}

	for _, secret := range []string{"DEVICE-ACCESS-SECRET", "DEVICE-REFRESH-SECRET"} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("stdout leaked a token:\n%s", out.String())
		}
	}
}

func TestConnect_AddOAuth_NoDCR_RequiresClientID(t *testing.T) {
	mcpURL, _ := newMockOAuthMCPNoDCR(t)

	orig := oauth.OpenBrowser
	oauth.OpenBrowser = headlessOpen(t)
	t.Cleanup(func() { oauth.OpenBrowser = orig })

	store := connector.NewStore(t.TempDir())
	c := NewCmd(&app.App{Connections: store})
	c.SetArgs([]string{"add", "my-mcp", "--connector", "mcp", "--url", mcpURL})

	err := c.Execute()
	if err == nil {
		t.Fatal("expected an error when the server has no DCR and no --client-id was given")
	}
	if !strings.Contains(err.Error(), "--client-id") {
		t.Errorf("error should point at --client-id, got: %v", err)
	}
}

// newMockOAuthMCPNoDCR is like newMockOAuthMCP but the AS advertises no
// registration_endpoint, so Register must fail with ErrNoDCR.
func newMockOAuthMCPNoDCR(t *testing.T) (mcpURL string, asURL string) {
	t.Helper()
	var as *httptest.Server
	as = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": as.URL + "/authorize",
				"token_endpoint":         as.URL + "/token",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(as.Close)

	var mcp *httptest.Server
	mcp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{as.URL}})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+mcp.URL+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(mcp.Close)

	return mcp.URL, as.URL
}

// newMockOAuthMCPManualClient is like newMockOAuthMCP but the AS advertises
// no registration_endpoint (a DCR attempt would 404 and fail with ErrNoDCR)
// — it exists to prove the manual --client-id/--client-secret path in `add`
// completes the browser + token exchange, and that the supplied client_id
// is the one actually sent to the token endpoint, without ever needing
// Dynamic Client Registration.
func newMockOAuthMCPManualClient(t *testing.T) (mcpURL string) {
	t.Helper()
	var mu sync.Mutex
	var wantChallenge string

	var as *httptest.Server
	as = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": as.URL + "/authorize",
				"token_endpoint":         as.URL + "/token",
			})
		case "/authorize":
			q := r.URL.Query()
			mu.Lock()
			wantChallenge = q.Get("code_challenge")
			mu.Unlock()
			dest := q.Get("redirect_uri") + "?" + url.Values{
				"code":  {"MANUALCODE"},
				"state": {q.Get("state")},
			}.Encode()
			http.Redirect(w, r, dest, http.StatusFound)
		case "/token":
			_ = r.ParseForm()
			verifier := r.FormValue("code_verifier")
			mu.Lock()
			challenge := wantChallenge
			mu.Unlock()
			sum := sha256.Sum256([]byte(verifier))
			gotChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
			if r.FormValue("grant_type") != "authorization_code" || r.FormValue("code") != "MANUALCODE" ||
				r.FormValue("client_id") != "manual-client" ||
				verifier == "" || challenge == "" || gotChallenge != challenge {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "MANUAL-ACCESS",
				"refresh_token": "MANUAL-REFRESH",
				"expires_in":    3600,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(as.Close)

	var mcp *httptest.Server
	mcp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{as.URL}})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+mcp.URL+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(mcp.Close)

	return mcp.URL
}

// TestConnect_AddOAuth_ManualClientID_SkipsDCR covers the second half of
// addViaOAuth's `if clientID == ""` branch: when --client-id (and
// --client-secret) are supplied, Register/DCR must never be attempted, and
// the manually-supplied credentials are what get persisted and sent to the
// token endpoint. The mock AS has no working /register endpoint at all, so
// if the code mistakenly called Register anyway, the command would fail
// with the "needs a pre-registered OAuth client" error instead of succeeding.
func TestConnect_AddOAuth_ManualClientID_SkipsDCR(t *testing.T) {
	mcpURL := newMockOAuthMCPManualClient(t)

	orig := oauth.OpenBrowser
	oauth.OpenBrowser = headlessOpen(t)
	t.Cleanup(func() { oauth.OpenBrowser = orig })

	store := connector.NewStore(t.TempDir())
	c := NewCmd(&app.App{Connections: store})
	c.SetArgs([]string{
		"add", "my-mcp", "--connector", "mcp", "--url", mcpURL,
		"--client-id", "manual-client", "--client-secret", "manual-secret",
	})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	mat, ok, err := store.LoadOAuth("my-mcp")
	if err != nil {
		t.Fatalf("LoadOAuth: %v", err)
	}
	if !ok {
		t.Fatal("expected oauth.json to be written, found none")
	}
	if mat.ClientID != "manual-client" {
		t.Errorf("ClientID = %q, want the manually-supplied client id (DCR must be skipped)", mat.ClientID)
	}
	if mat.ClientSecret != "manual-secret" {
		t.Errorf("ClientSecret = %q, want the manually-supplied secret", mat.ClientSecret)
	}
	if mat.AccessToken != "MANUAL-ACCESS" || mat.RefreshToken != "MANUAL-REFRESH" {
		t.Errorf("tokens = %+v, want the AS's issued grant", mat)
	}
}

// TestConnect_Add_NoOAuthNoToken_StaticTokenError covers the discovery
// branch where the service isn't OAuth-protected (Discover returns
// oauth.ErrNoOAuth because the unauthenticated probe never 401s) and no
// --token/stdin token was supplied either: `add` must surface the
// user-facing "pass --token or pipe it on stdin" message, not a raw
// discovery error.
func TestConnect_Add_NoOAuthNoToken_StaticTokenError(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(plain.Close)

	store := connector.NewStore(t.TempDir())
	c := NewCmd(&app.App{Connections: store})
	c.SetArgs([]string{"add", "my-svc", "--connector", "generic", "--url", plain.URL})
	c.SetIn(strings.NewReader("")) // nothing piped on stdin

	err := c.Execute()
	if err == nil {
		t.Fatal("expected an error: no OAuth and no token")
	}
	const want = "no token - pass --token or pipe it on stdin"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}

	if _, getErr := store.Get("my-svc"); getErr == nil {
		t.Error("no connection should have been persisted on this failure path")
	}
}

func TestConnect_Reauth_HeadlessBrowserFlow(t *testing.T) {
	mcpURL, _ := newMockOAuthMCP(t)

	orig := oauth.OpenBrowser
	oauth.OpenBrowser = headlessOpen(t)
	t.Cleanup(func() { oauth.OpenBrowser = orig })

	store := connector.NewStore(t.TempDir())
	if _, err := store.Create("my-mcp", "mcp", mcpURL, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveOAuth("my-mcp", connector.OAuthMaterial{
		AccessToken:   "STALE-ACCESS",
		RefreshToken:  "STALE-REFRESH",
		TokenEndpoint: "http://stale/token",
		ClientID:      "dyn-client",
		ClientSecret:  "dyn-secret",
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	c := NewCmd(&app.App{Connections: store})
	c.SetOut(&out)
	c.SetArgs([]string{"reauth", "my-mcp"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	mat, ok, err := store.LoadOAuth("my-mcp")
	if err != nil || !ok {
		t.Fatalf("LoadOAuth after reauth: ok=%v err=%v", ok, err)
	}
	if mat.AccessToken != "ACCESS-XYZ-SECRET" || mat.RefreshToken != "REFRESH-XYZ-SECRET" {
		t.Errorf("reauth did not refresh tokens: %+v", mat)
	}
	if mat.ClientID != "dyn-client" {
		t.Errorf("reauth should reuse the stored ClientID, got %q", mat.ClientID)
	}
	if mat.RotatedAt.IsZero() {
		t.Error("RotatedAt not stamped when reauth sealed a fresh grant")
	}
	if strings.Contains(out.String(), "ACCESS-XYZ-SECRET") || strings.Contains(out.String(), "REFRESH-XYZ-SECRET") {
		t.Errorf("stdout leaked a token:\n%s", out.String())
	}
}
