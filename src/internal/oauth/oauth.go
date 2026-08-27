// Package oauth is a minimal OAuth 2.1 client for brokered MCP: PKCE, .well-known
// metadata discovery (RFC 9728 + RFC 8414), Dynamic Client Registration (RFC 7591),
// authorization-code exchange, and refresh. Standard library only.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrNoOAuth = errors.New("oauth: server is not OAuth-protected")
	ErrNoDCR   = errors.New("oauth: server does not support dynamic client registration")
)

type Token struct {
	AccessToken  string
	RefreshToken string
	Scope        string
	ExpiresAt    time.Time
}

type Metadata struct {
	AuthorizationEndpoint       string
	TokenEndpoint               string
	RegistrationEndpoint        string
	DeviceAuthorizationEndpoint string
}

func PKCE() (verifier, challenge string, err error) {
	b := make([]byte, 40)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func BuildAuthURL(m Metadata, clientID, redirectURI, challenge, state, scope string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	sep := "?"
	if strings.Contains(m.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return m.AuthorizationEndpoint + sep + q.Encode()
}

// tokenResp decodes an OAuth token response and converts expires_in to ExpiresAt.
type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
}

func (tr tokenResp) token() Token {
	exp := time.Time{}
	if tr.ExpiresIn > 0 {
		exp = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return Token{AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken, Scope: tr.Scope, ExpiresAt: exp}
}

// parseTokenResponse decodes a 2xx token-endpoint body (shared shape between
// authorization_code/refresh_token/device_code grants) into a Token.
func parseTokenResponse(body []byte) (Token, error) {
	var tr tokenResp
	if err := json.Unmarshal(body, &tr); err != nil {
		return Token{}, fmt.Errorf("oauth: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return Token{}, errors.New("oauth: token response had no access_token")
	}
	return tr.token(), nil
}

func postForm(ctx context.Context, hc *http.Client, endpoint string, form url.Values, clientID, clientSecret string) (Token, error) {
	form.Set("client_id", clientID)
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return Token{}, fmt.Errorf("oauth: token endpoint %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseTokenResponse(body)
}

func Exchange(ctx context.Context, hc *http.Client, m Metadata, clientID, clientSecret, code, verifier, redirectURI string) (Token, error) {
	return postForm(ctx, hc, m.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	}, clientID, clientSecret)
}

func Refresh(ctx context.Context, hc *http.Client, tokenEndpoint, refreshToken, clientID, clientSecret string) (Token, error) {
	tok, err := postForm(ctx, hc, tokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}, clientID, clientSecret)
	if err != nil {
		return Token{}, err
	}
	if tok.RefreshToken == "" { // non-rotating provider — keep the old one
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

func getJSON(ctx context.Context, hc *http.Client, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("oauth: GET %s: %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(into)
}

func Discover(ctx context.Context, hc *http.Client, mcpURL string) (Metadata, error) {
	// 1. Probe unauthenticated; only a 401 means OAuth-protected.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mcpURL, nil)
	if err != nil {
		return Metadata{}, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Metadata{}, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return Metadata{}, ErrNoOAuth
	}
	origin, err := originOf(mcpURL)
	if err != nil {
		return Metadata{}, err
	}
	// 2. protected-resource metadata → authorization server.
	prmURL := resourceMetadataURL(resp.Header.Get("WWW-Authenticate"), origin)
	var prm struct {
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := getJSON(ctx, hc, prmURL, &prm); err != nil || len(prm.AuthorizationServers) == 0 {
		return Metadata{}, fmt.Errorf("oauth: no protected-resource metadata: %w", err)
	}
	as := strings.TrimRight(prm.AuthorizationServers[0], "/")
	// 3. authorization-server metadata.
	var asm struct {
		AuthorizationEndpoint       string `json:"authorization_endpoint"`
		TokenEndpoint               string `json:"token_endpoint"`
		RegistrationEndpoint        string `json:"registration_endpoint"`
		DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	}
	if err := getJSON(ctx, hc, as+"/.well-known/oauth-authorization-server", &asm); err != nil {
		if err2 := getJSON(ctx, hc, as+"/.well-known/openid-configuration", &asm); err2 != nil {
			return Metadata{}, fmt.Errorf("oauth: no authorization-server metadata: %w", err)
		}
	}
	if asm.TokenEndpoint == "" || asm.AuthorizationEndpoint == "" {
		return Metadata{}, errors.New("oauth: authorization-server metadata missing endpoints")
	}
	return Metadata{
		AuthorizationEndpoint:       asm.AuthorizationEndpoint,
		TokenEndpoint:               asm.TokenEndpoint,
		RegistrationEndpoint:        asm.RegistrationEndpoint,
		DeviceAuthorizationEndpoint: asm.DeviceAuthorizationEndpoint,
	}, nil
}

func Register(ctx context.Context, hc *http.Client, registrationEndpoint, redirectURI string) (clientID, clientSecret string, err error) {
	if registrationEndpoint == "" {
		return "", "", ErrNoDCR
	}
	body, _ := json.Marshal(map[string]any{
		"client_name":                "poddle",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", "", ErrNoDCR
	}
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil || out.ClientID == "" {
		return "", "", fmt.Errorf("oauth: DCR response invalid: %w", err)
	}
	return out.ClientID, out.ClientSecret, nil
}

func originOf(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	return u.Scheme + "://" + u.Host, nil
}

// resourceMetadataURL prefers the resource_metadata pointer in a WWW-Authenticate
// header, falling back to the conventional well-known path on the origin.
func resourceMetadataURL(wwwAuth, origin string) string {
	if i := strings.Index(wwwAuth, `resource_metadata="`); i >= 0 {
		rest := wwwAuth[i+len(`resource_metadata="`):]
		if j := strings.IndexByte(rest, '"'); j >= 0 {
			return rest[:j]
		}
	}
	return origin + "/.well-known/oauth-protected-resource"
}
