package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPKCE_S256(t *testing.T) {
	v, c, err := PKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(v) < 43 || len(v) > 128 {
		t.Errorf("verifier length %d out of RFC 7636 range", len(v))
	}
	// challenge = base64url(sha256(verifier)), no padding.
	if strings.ContainsAny(c, "=+/") {
		t.Errorf("challenge %q must be base64url without padding", c)
	}
	if v == c {
		t.Error("challenge must be the S256 hash, not the verifier")
	}
}

func TestExchange_And_Refresh(t *testing.T) {
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.FormValue("grant_type") {
		case "authorization_code":
			if r.FormValue("code") != "the-code" || r.FormValue("code_verifier") == "" {
				w.WriteHeader(400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at1", "refresh_token": "rt1", "expires_in": 3600, "scope": "mcp"})
		case "refresh_token":
			if r.FormValue("refresh_token") != "rt1" {
				w.WriteHeader(401)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at2", "refresh_token": "rt2", "expires_in": 3600})
		default:
			w.WriteHeader(400)
		}
	}))
	defer as.Close()
	m := Metadata{TokenEndpoint: as.URL + "/token"}

	tok, err := Exchange(context.Background(), as.Client(), m, "cid", "", "the-code", "verifier", "http://127.0.0.1:1/cb")
	if err != nil || tok.AccessToken != "at1" || tok.RefreshToken != "rt1" {
		t.Fatalf("exchange = %+v, %v", tok, err)
	}
	if tok.ExpiresAt.IsZero() {
		t.Error("ExpiresAt must be set from expires_in")
	}
	tok2, err := Refresh(context.Background(), as.Client(), m.TokenEndpoint, "rt1", "cid", "")
	if err != nil || tok2.AccessToken != "at2" || tok2.RefreshToken != "rt2" {
		t.Fatalf("refresh = %+v, %v", tok2, err)
	}
}

func TestRefresh_FailurePropagates(t *testing.T) {
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer as.Close()
	if _, err := Refresh(context.Background(), as.Client(), as.URL, "dead", "cid", ""); err == nil {
		t.Fatal("a 401 from the token endpoint must be an error")
	}
}

// TestRefresh_CarriesForwardNonRotatingToken covers oauth.go:132-133: a
// non-rotating provider's token response omits refresh_token, and Refresh
// must carry the caller's old refresh token forward rather than clearing it
// — load-bearing for the v1 "non-rotating providers are unaffected" story.
func TestRefresh_CarriesForwardNonRotatingToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at2", "expires_in": 3600})
	}))
	defer srv.Close()

	tok, err := Refresh(context.Background(), http.DefaultClient, srv.URL, "rt-old", "", "")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tok.AccessToken != "at2" {
		t.Errorf("AccessToken = %q, want at2", tok.AccessToken)
	}
	if tok.RefreshToken != "rt-old" {
		t.Errorf("RefreshToken = %q, want rt-old (carried forward, provider omitted it)", tok.RefreshToken)
	}
}

func TestDiscover_ChainAndErrNoOAuth(t *testing.T) {
	var as *httptest.Server
	as = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": as.URL + "/authorize",
				"token_endpoint":         as.URL + "/token",
				"registration_endpoint":  as.URL + "/register",
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
	if md.TokenEndpoint != as.URL+"/token" || md.AuthorizationEndpoint != as.URL+"/authorize" {
		t.Errorf("metadata = %+v", md)
	}

	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer plain.Close()
	if _, err := Discover(context.Background(), plain.Client(), plain.URL); err != ErrNoOAuth {
		t.Errorf("plain 200 must give ErrNoOAuth, got %v", err)
	}
}

func mcpBase(r *http.Request) string { u := url.URL{Scheme: "http", Host: r.Host}; return u.String() }

// TestRegister_NonSuccessStatus_ErrNoDCR documents CURRENT behavior (M1):
// Register collapses ANY non-2xx registration-endpoint response — whether
// it's a hard 500 or a routing-mistake 404 — into the single sentinel
// ErrNoDCR, same as an endpoint that was never advertised at all. This test
// does not assert that collapse is desirable, only that it's what ships;
// do not change Register to differentiate status codes without updating
// this test and its caller in cli/connect (which surfaces one fixed
// "needs pre-registered client" message for ErrNoDCR either way).
func TestRegister_NonSuccessStatus_ErrNoDCR(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusNotFound} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer as.Close()

			clientID, clientSecret, err := Register(context.Background(), as.Client(), as.URL+"/register", "http://127.0.0.1/callback")
			if !errors.Is(err, ErrNoDCR) {
				t.Fatalf("Register (status %d): err = %v, want ErrNoDCR", status, err)
			}
			if clientID != "" || clientSecret != "" {
				t.Errorf("Register (status %d): clientID=%q clientSecret=%q, want both empty on error", status, clientID, clientSecret)
			}
		})
	}
}
