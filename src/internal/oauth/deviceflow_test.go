package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// deviceAuthHandler returns a handler for the /device_authorization endpoint
// that always succeeds with the given interval/expiry.
func deviceAuthHandler(interval, expiresIn int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "DEVCODE123",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://as/activate",
			"interval":         interval,
			"expires_in":       expiresIn,
		})
	}
}

// TestDeviceFlow_SuccessAfterPending covers the RFC 8628 happy path: the
// device-authorization endpoint hands back a device code + user code, and
// the token endpoint reports authorization_pending for the first couple of
// polls before the user completes consent out-of-band and it returns a
// token.
func TestDeviceFlow_SuccessAfterPending(t *testing.T) {
	var pendingLeft int32 = 2
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device_authorization":
			deviceAuthHandler(1, 300)(w, r)
		case "/token":
			_ = r.ParseForm()
			if r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.FormValue("device_code") != "DEVCODE123" {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			if atomic.AddInt32(&pendingLeft, -1) >= 0 {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at1", "refresh_token": "rt1", "expires_in": 3600, "scope": "mcp"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer as.Close()

	m := Metadata{DeviceAuthorizationEndpoint: as.URL + "/device_authorization", TokenEndpoint: as.URL + "/token"}

	var mu sync.Mutex
	var gotURI, gotCode string
	display := func(verificationURI, userCode string) {
		mu.Lock()
		defer mu.Unlock()
		gotURI, gotCode = verificationURI, userCode
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	var tok Token
	var err error
	go func() {
		tok, err = DeviceFlow(ctx, as.Client(), m, "cid", "", "mcp", display)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("DeviceFlow did not return after authorization completed")
	}

	if err != nil {
		t.Fatalf("DeviceFlow: %v", err)
	}
	if tok.AccessToken != "at1" || tok.RefreshToken != "rt1" || tok.Scope != "mcp" {
		t.Errorf("token = %+v", tok)
	}
	if tok.ExpiresAt.IsZero() {
		t.Error("ExpiresAt must be set from expires_in")
	}

	mu.Lock()
	defer mu.Unlock()
	if gotURI != "https://as/activate" {
		t.Errorf("display verificationURI = %q, want https://as/activate", gotURI)
	}
	if gotCode != "WDJB-MJHT" {
		t.Errorf("display userCode = %q, want WDJB-MJHT", gotCode)
	}
}

// TestDeviceFlow_VerificationURIComplete asserts display prefers
// verification_uri_complete for the URL when the AS provides it, while still
// passing the plain user_code.
func TestDeviceFlow_VerificationURIComplete(t *testing.T) {
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device_authorization":
			json.NewEncoder(w).Encode(map[string]any{
				"device_code":               "DEVCODE123",
				"user_code":                 "WDJB-MJHT",
				"verification_uri":          "https://as/activate",
				"verification_uri_complete": "https://as/activate?user_code=WDJB-MJHT",
				"interval":                  1,
				"expires_in":                300,
			})
		case "/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at1", "expires_in": 3600})
		default:
			w.WriteHeader(404)
		}
	}))
	defer as.Close()

	m := Metadata{DeviceAuthorizationEndpoint: as.URL + "/device_authorization", TokenEndpoint: as.URL + "/token"}

	var mu sync.Mutex
	var gotURI, gotCode string
	display := func(verificationURI, userCode string) {
		mu.Lock()
		defer mu.Unlock()
		gotURI, gotCode = verificationURI, userCode
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_, _ = DeviceFlow(ctx, as.Client(), m, "cid", "", "", display)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("DeviceFlow did not return")
	}

	mu.Lock()
	defer mu.Unlock()
	if gotURI != "https://as/activate?user_code=WDJB-MJHT" {
		t.Errorf("display verificationURI = %q, want the verification_uri_complete", gotURI)
	}
	if gotCode != "WDJB-MJHT" {
		t.Errorf("display userCode = %q, want the plain user_code", gotCode)
	}
}

// TestDeviceFlow_SlowDown covers RFC 8628 §3.5's slow_down response: the
// poll interval must back off by 5s and the flow must still succeed once the
// AS reports a token.
func TestDeviceFlow_SlowDown(t *testing.T) {
	var slowedDown int32
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device_authorization":
			deviceAuthHandler(1, 300)(w, r)
		case "/token":
			if atomic.CompareAndSwapInt32(&slowedDown, 0, 1) {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]string{"error": "slow_down"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at1", "expires_in": 3600})
		default:
			w.WriteHeader(404)
		}
	}))
	defer as.Close()

	m := Metadata{DeviceAuthorizationEndpoint: as.URL + "/device_authorization", TokenEndpoint: as.URL + "/token"}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	done := make(chan struct{})
	var tok Token
	var err error
	go func() {
		tok, err = DeviceFlow(ctx, as.Client(), m, "cid", "", "", func(string, string) {})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("DeviceFlow did not return after slow_down backoff")
	}

	if err != nil {
		t.Fatalf("DeviceFlow: %v", err)
	}
	if tok.AccessToken != "at1" {
		t.Errorf("AccessToken = %q, want at1", tok.AccessToken)
	}
}

// TestDeviceFlow_AccessDenied covers a terminal error code (anything other
// than authorization_pending/slow_down) aborting the poll loop immediately.
func TestDeviceFlow_AccessDenied(t *testing.T) {
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device_authorization":
			deviceAuthHandler(1, 300)(w, r)
		case "/token":
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer as.Close()

	m := Metadata{DeviceAuthorizationEndpoint: as.URL + "/device_authorization", TokenEndpoint: as.URL + "/token"}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	var err error
	go func() {
		_, err = DeviceFlow(ctx, as.Client(), m, "cid", "", "", func(string, string) {})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("DeviceFlow did not return after access_denied")
	}

	if err == nil {
		t.Fatal("access_denied must be a terminal error")
	}
	if !strings.Contains(err.Error(), "access_denied") {
		t.Errorf("error should mention access_denied, got: %v", err)
	}
}

// TestDeviceFlow_CtxTimeout mirrors TestAuthCodeFlow_CtxTimeout: a token
// endpoint that never stops reporting authorization_pending, bounded by a
// short ctx, must return a ctx error rather than poll forever.
func TestDeviceFlow_CtxTimeout(t *testing.T) {
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device_authorization":
			deviceAuthHandler(1, 300)(w, r)
		case "/token":
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer as.Close()

	m := Metadata{DeviceAuthorizationEndpoint: as.URL + "/device_authorization", TokenEndpoint: as.URL + "/token"}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	var err error
	go func() {
		_, err = DeviceFlow(ctx, as.Client(), m, "cid", "", "", func(string, string) {})
		close(done)
	}()

	select {
	case <-done:
		if err == nil {
			t.Fatal("expected a ctx-deadline error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DeviceFlow did not return after ctx timeout")
	}
}

// TestDeviceFlow_NoDeviceAuthorizationEndpoint covers the guard for an
// authorization server whose metadata didn't advertise device support.
func TestDeviceFlow_NoDeviceAuthorizationEndpoint(t *testing.T) {
	_, err := DeviceFlow(context.Background(), http.DefaultClient, Metadata{}, "cid", "", "", func(string, string) {})
	if err == nil {
		t.Fatal("expected an error when DeviceAuthorizationEndpoint is empty")
	}
	if !strings.Contains(err.Error(), "device_authorization_endpoint") {
		t.Errorf("error should mention device_authorization_endpoint, got: %v", err)
	}
}

// TestDeviceFlow_InitiateNonSuccessStatus covers a device-authorization
// endpoint that itself rejects the request (e.g. an unknown client_id).
func TestDeviceFlow_InitiateNonSuccessStatus(t *testing.T) {
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
	}))
	defer as.Close()

	m := Metadata{DeviceAuthorizationEndpoint: as.URL, TokenEndpoint: as.URL + "/token"}
	_, err := DeviceFlow(context.Background(), as.Client(), m, "cid", "", "", func(string, string) {})
	if err == nil {
		t.Fatal("a non-2xx device authorization response must be an error")
	}
}

// TestDiscover_DeviceAuthorizationEndpoint mirrors
// TestDiscover_ChainAndErrNoOAuth, asserting the additive
// device_authorization_endpoint field is parsed out of AS metadata.
func TestDiscover_DeviceAuthorizationEndpoint(t *testing.T) {
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
		default:
			w.WriteHeader(404)
		}
	}))
	defer as.Close()
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{as.URL}})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+mcpBase(r)+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(401)
	}))
	defer mcp.Close()

	md, err := Discover(context.Background(), mcp.Client(), mcp.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if md.DeviceAuthorizationEndpoint != as.URL+"/device_authorization" {
		t.Errorf("DeviceAuthorizationEndpoint = %q, want %q", md.DeviceAuthorizationEndpoint, as.URL+"/device_authorization")
	}
}
