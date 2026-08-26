package oauth

import (
	"context"
	"encoding/json"
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
