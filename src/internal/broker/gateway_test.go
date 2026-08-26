package broker

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordedReq captures what the upstream (fake vendor) actually received.
type recordedReq struct {
	method, path, query, auth, apikey, googkey, body string
}

// upstreamRecording starts a fake vendor that records the request it sees.
func upstreamRecording(t *testing.T) (*httptest.Server, *recordedReq) {
	t.Helper()
	rec := &recordedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.query = r.URL.RawQuery
		rec.auth = r.Header.Get("Authorization")
		rec.apikey = r.Header.Get("X-Api-Key")
		rec.googkey = r.Header.Get("X-Goog-Api-Key")
		rec.body = string(body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// gatewayWith builds a gateway holding one credential and returns the gateway
// plus a valid handle for it.
func gatewayWith(t *testing.T, cred Credential) (*Gateway, string) {
	t.Helper()
	v := NewVault()
	h := NewHandles(v)
	id, err := v.Store("local", cred)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	handle, err := h.IssueHandle("local", id, "box", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return NewGateway(h), handle.Value
}

func serve(t *testing.T, g *Gateway) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return srv
}

// do sends a request through the gateway (handle in Authorization if non-empty)
// and returns the status, fully draining the body first.
func do(t *testing.T, gw *httptest.Server, handleVal, method, target string, body io.Reader) int {
	t.Helper()
	req, err := http.NewRequest(method, gw.URL+target, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if handleVal != "" {
		req.Header.Set("Authorization", "Bearer "+handleVal)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// doResp sends a request through the gateway (handle in Authorization if
// non-empty) and returns the raw *http.Response so callers can assert on
// headers/status; the body is closed on test cleanup.
func doResp(t *testing.T, gw *httptest.Server, handleVal, method, target string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, gw.URL+target, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if handleVal != "" {
		req.Header.Set("Authorization", "Bearer "+handleVal)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestGateway_SubscriptionInjectsBearer(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "realtok", BaseURL: up.URL})
	gw := serve(t, g)

	if code := do(t, gw, handle, http.MethodGet, "/v1/messages", nil); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if rec.auth != "Bearer realtok" {
		t.Errorf("upstream Authorization = %q, want %q", rec.auth, "Bearer realtok")
	}
	if strings.Contains(rec.auth, handle) {
		t.Errorf("handle leaked to upstream: %q", rec.auth)
	}
	if rec.apikey != "" {
		t.Errorf("unexpected X-Api-Key = %q", rec.apikey)
	}
}

func TestGateway_APIKeyInjectsXApiKey(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, handle := gatewayWith(t, Credential{Mode: ModeAPIKey, Secret: "realkey", BaseURL: up.URL})
	gw := serve(t, g)

	if code := do(t, gw, handle, http.MethodGet, "/v1/messages", nil); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if rec.apikey != "realkey" {
		t.Errorf("upstream X-Api-Key = %q, want %q", rec.apikey, "realkey")
	}
	if rec.auth != "" {
		t.Errorf("Authorization should be cleared (handle stripped), got %q", rec.auth)
	}
}

// TestGateway_GoogleAPIKeyInjectsXGoogApiKey covers the Gemini path. gemini-cli
// in bearer mode presents the handle in BOTH Authorization: Bearer (which the
// gateway reads to resolve the handle) AND X-Goog-Api-Key (its SDK sends it
// regardless). applyAuth must strip both and inject the real key as
// X-Goog-Api-Key — the header Google's endpoint expects.
func TestGateway_GoogleAPIKeyInjectsXGoogApiKey(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, handle := gatewayWith(t, Credential{Mode: ModeGoogleAPIKey, Vendor: "google", Secret: "realgkey", BaseURL: up.URL})
	gw := serve(t, g)

	// Send the handle in both headers, exactly as gemini-cli's bearer mode does.
	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1beta/models/gemini-2.5-flash:streamGenerateContent", nil)
	req.Header.Set("Authorization", "Bearer "+handle)
	req.Header.Set("X-Goog-Api-Key", handle)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if rec.googkey != "realgkey" {
		t.Errorf("upstream X-Goog-Api-Key = %q, want %q", rec.googkey, "realgkey")
	}
	if rec.auth != "" {
		t.Errorf("Authorization should be cleared (handle stripped), got %q", rec.auth)
	}
	if strings.Contains(rec.googkey, handle) || strings.Contains(rec.auth, handle) {
		t.Errorf("handle leaked to upstream: auth=%q goog=%q", rec.auth, rec.googkey)
	}
	// Defense-in-depth: the handle must never ride the query string either (Google's
	// legacy ?key= form). applyAuth only rewrites headers, so this guards against a
	// future client putting the secret in the URL.
	if strings.Contains(rec.query, handle) {
		t.Errorf("handle leaked to upstream via query string: %q", rec.query)
	}
}

func TestGateway_EndpointWithSecretInjectsBearer(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, handle := gatewayWith(t, Credential{Mode: ModeEndpoint, Secret: "epkey", BaseURL: up.URL})
	gw := serve(t, g)

	if code := do(t, gw, handle, http.MethodGet, "/x", nil); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if rec.auth != "Bearer epkey" {
		t.Errorf("upstream Authorization = %q, want %q", rec.auth, "Bearer epkey")
	}
}

func TestGateway_EndpointNoSecretSendsNoAuth(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, handle := gatewayWith(t, Credential{Mode: ModeEndpoint, Secret: "", BaseURL: up.URL})
	gw := serve(t, g)

	if code := do(t, gw, handle, http.MethodGet, "/x", nil); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if rec.auth != "" {
		t.Errorf("no auth expected, got Authorization = %q", rec.auth)
	}
	if rec.apikey != "" {
		t.Errorf("no auth expected, got X-Api-Key = %q", rec.apikey)
	}
}

func TestGateway_BasicMode_HandleFromUsername(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, handle := gatewayWith(t, Credential{Mode: ModeBasic, Vendor: "forgejo", Secret: "realuser:realtoken", BaseURL: up.URL})
	gw := serve(t, g)

	// git presents the handle as the Basic-auth username.
	creds := base64.StdEncoding.EncodeToString([]byte(handle + ":x"))
	req, _ := http.NewRequest(http.MethodGet, gw.URL+"/datadir/repo.git/info/refs", nil)
	req.Header.Set("Authorization", "Basic "+creds)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("realuser:realtoken"))
	if rec.auth != want {
		t.Errorf("upstream Authorization = %q, want %q", rec.auth, want)
	}
	if strings.Contains(rec.auth, handle) {
		t.Errorf("handle leaked to upstream: %q", rec.auth)
	}
}

func TestGateway_PreservesMethodPathQueryBody(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, handle := gatewayWith(t, Credential{Mode: ModeAPIKey, Secret: "k", BaseURL: up.URL})
	gw := serve(t, g)

	if code := do(t, gw, handle, http.MethodPost, "/v1/messages?beta=true", strings.NewReader(`{"hi":1}`)); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if rec.method != http.MethodPost {
		t.Errorf("method = %q, want POST", rec.method)
	}
	if rec.path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", rec.path)
	}
	if rec.query != "beta=true" {
		t.Errorf("query = %q, want beta=true", rec.query)
	}
	if rec.body != `{"hi":1}` {
		t.Errorf("body = %q, want %q", rec.body, `{"hi":1}`)
	}
}

func TestGateway_OAuthBearerInjectsAccessToken(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, handle := gatewayWith(t, Credential{Mode: ModeOAuthBearer, Vendor: "mcp", Secret: "access-tok", BaseURL: up.URL})
	gw := serve(t, g)
	if code := do(t, gw, handle, http.MethodPost, "/mcp", nil); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if rec.auth != "Bearer access-tok" {
		t.Errorf("upstream Authorization = %q, want Bearer access-tok", rec.auth)
	}
}

func TestGateway_RefreshesStaleOAuthToken(t *testing.T) {
	up, rec := upstreamRecording(t)
	// expired access token + a refresh func that mints a fresh one.
	cred := Credential{Mode: ModeOAuthBearer, Secret: "stale", RefreshToken: "r1",
		ExpiresAt: time.Now().Add(-time.Minute), BaseURL: up.URL, TokenEndpoint: "http://unused"}
	g, handle := gatewayWith(t, cred)
	g.refresh = func(_ context.Context, c Credential) (Credential, error) {
		c.Secret = "fresh"
		c.ExpiresAt = time.Now().Add(time.Hour)
		return c, nil
	}
	gw := serve(t, g)
	if code := do(t, gw, handle, http.MethodPost, "/mcp", nil); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if rec.auth != "Bearer fresh" {
		t.Errorf("upstream saw %q, want Bearer fresh (refreshed)", rec.auth)
	}
}

// persistCall records one Persist(connName, mirror) invocation.
type persistCall struct {
	conn string
	m    connMirror
}

// fakePersister is an OAuthPersister test double that records every Persist
// call instead of touching disk; safe for concurrent use.
type fakePersister struct {
	mu    sync.Mutex
	calls []persistCall
}

func (p *fakePersister) Persist(connName string, m connMirror) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, persistCall{conn: connName, m: m})
	return nil
}

func (p *fakePersister) all() []persistCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]persistCall(nil), p.calls...)
}

// TestGateway_PersistsOnRotation covers §T3: when a refresh actually rotates
// the refresh token, the new material is mirrored via the wired persister,
// keyed by the credential's WriteBackKey (the connection name).
func TestGateway_PersistsOnRotation(t *testing.T) {
	up, _ := upstreamRecording(t)
	cred := Credential{Mode: ModeOAuthBearer, Secret: "stale", RefreshToken: "r1",
		ExpiresAt: time.Now().Add(-time.Minute), BaseURL: up.URL, TokenEndpoint: "http://unused",
		WriteBackKey: "gh"}
	g, handle := gatewayWith(t, cred)
	g.refresh = func(_ context.Context, c Credential) (Credential, error) {
		c.Secret = "fresh"
		c.RefreshToken = "r2" // rotated
		c.ExpiresAt = time.Now().Add(time.Hour)
		return c, nil
	}
	fp := &fakePersister{}
	g.SetOAuthPersister(fp)
	gw := serve(t, g)

	if code := do(t, gw, handle, http.MethodPost, "/mcp", nil); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	calls := fp.all()
	if len(calls) != 1 {
		t.Fatalf("persist called %d times, want 1: %+v", len(calls), calls)
	}
	if calls[0].conn != "gh" {
		t.Errorf("Persist connName = %q, want %q", calls[0].conn, "gh")
	}
	if calls[0].m.RefreshToken != "r2" {
		t.Errorf("persisted RefreshToken = %q, want %q (rotated)", calls[0].m.RefreshToken, "r2")
	}
}

// TestGateway_NoPersistWithoutRotation covers §T3: a refresh that mints the
// same refresh token (an access-token-only rotation, common for some
// providers) must NOT trigger a write-back — nothing changed on disk.
func TestGateway_NoPersistWithoutRotation(t *testing.T) {
	up, _ := upstreamRecording(t)
	cred := Credential{Mode: ModeOAuthBearer, Secret: "stale", RefreshToken: "r1",
		ExpiresAt: time.Now().Add(-time.Minute), BaseURL: up.URL, TokenEndpoint: "http://unused",
		WriteBackKey: "gh"}
	g, handle := gatewayWith(t, cred)
	g.refresh = func(_ context.Context, c Credential) (Credential, error) {
		c.Secret = "fresh"
		c.ExpiresAt = time.Now().Add(time.Hour) // RefreshToken left as "r1" — unrotated
		return c, nil
	}
	fp := &fakePersister{}
	g.SetOAuthPersister(fp)
	gw := serve(t, g)

	if code := do(t, gw, handle, http.MethodPost, "/mcp", nil); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if calls := fp.all(); len(calls) != 0 {
		t.Errorf("persist called %d times, want 0 (no rotation): %+v", len(calls), calls)
	}
}

// TestGateway_FlagsNeedsReauthOnFailure covers §T3: a refresh failure flags
// the connection in NeedsReauth(), and the request still fails closed with the
// bare 401 (unchanged from TestGateway_RefreshFailureIsFailClosed401).
func TestGateway_FlagsNeedsReauthOnFailure(t *testing.T) {
	up, _ := upstreamRecording(t)
	cred := Credential{Mode: ModeOAuthBearer, Secret: "stale", RefreshToken: "dead",
		ExpiresAt: time.Now().Add(-time.Minute), BaseURL: up.URL, WriteBackKey: "gh"}
	g, handle := gatewayWith(t, cred)
	g.refresh = func(context.Context, Credential) (Credential, error) {
		return Credential{}, errors.New("refresh failed")
	}
	gw := serve(t, g)

	resp := doResp(t, gw, handle, http.MethodPost, "/mcp")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") != "" {
		t.Error("fail-closed 401 must NOT carry WWW-Authenticate (don't trigger the agent's own OAuth)")
	}
	got := g.NeedsReauth()
	found := false
	for _, k := range got {
		if k == "gh" {
			found = true
		}
	}
	if !found {
		t.Errorf("NeedsReauth() = %v, want it to contain %q", got, "gh")
	}
}

func TestGateway_RefreshFailureIsFailClosed401(t *testing.T) {
	up, _ := upstreamRecording(t)
	cred := Credential{Mode: ModeOAuthBearer, Secret: "stale", RefreshToken: "dead",
		ExpiresAt: time.Now().Add(-time.Minute), BaseURL: up.URL}
	g, handle := gatewayWith(t, cred)
	g.refresh = func(context.Context, Credential) (Credential, error) {
		return Credential{}, errors.New("refresh failed")
	}
	aud := &recAuditor{}
	g.SetAuditor(aud)
	gw := serve(t, g)
	resp := doResp(t, gw, handle, http.MethodPost, "/mcp") // helper returning *http.Response
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") != "" {
		t.Error("fail-closed 401 must NOT carry WWW-Authenticate (don't trigger the agent's own OAuth)")
	}
	// §4: this path must be audited, secret-free, so operators see a signal that
	// the credential needs `connect reauth`.
	recs := aud.all()
	if len(recs) != 1 {
		t.Fatalf("expected exactly one audit record for the refresh-failure 401, got %d: %+v", len(recs), recs)
	}
	rec := recs[0]
	if rec.Decision != "deny" || rec.Status != http.StatusUnauthorized {
		t.Errorf("audit record = %+v, want Decision=deny Status=401", rec)
	}
	for _, secret := range []string{"stale", "dead"} {
		if strings.Contains(rec.Detail, secret) {
			t.Errorf("audit detail leaked a secret: %+v", rec)
		}
	}
	if strings.Contains(rec.Detail, handle) {
		t.Errorf("audit detail leaked the handle: %+v", rec)
	}
}

func TestGateway_InvalidHandle401(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, _ := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "x", BaseURL: up.URL})
	gw := serve(t, g)

	if code := do(t, gw, "poddle_bogus", http.MethodGet, "/x", nil); code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", code)
	}
	if rec.method != "" {
		t.Errorf("upstream should not be hit for an invalid handle, saw method %q", rec.method)
	}
}

// TestGateway_InvalidHandleChallengesBasic guards against a regression where an
// unresolvable/absent handle's 401 lost its WWW-Authenticate: Basic challenge.
// Git probes unauthenticated first and only retries with the pod's handle as
// the Basic username after seeing this challenge, so it must always be
// present on this path — unlike the OAuth refresh-failure 401, which must
// stay bare (see TestGateway_RefreshFailureIsFailClosed401).
func TestGateway_InvalidHandleChallengesBasic(t *testing.T) {
	up, _ := upstreamRecording(t)
	g, _ := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "x", BaseURL: up.URL})
	gw := serve(t, g)

	resp := doResp(t, gw, "poddle_bogus", http.MethodGet, "/x")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got, want := resp.Header.Get("WWW-Authenticate"), `Basic realm="poddle"`; got != want {
		t.Errorf("WWW-Authenticate = %q, want %q", got, want)
	}
}

func TestGateway_RevokedHandle401(t *testing.T) {
	up, _ := upstreamRecording(t)
	v := NewVault()
	h := NewHandles(v)
	id, _ := v.Store("local", Credential{Mode: ModeSubscription, Secret: "x", BaseURL: up.URL})
	handle, _ := h.IssueHandle("local", id, "box", time.Hour)
	h.Revoke(handle.Value)
	gw := serve(t, NewGateway(h))

	if code := do(t, gw, handle.Value, http.MethodGet, "/x", nil); code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", code)
	}
}

// TestGateway_ConcurrentRefreshCollapsesToOne proves the §5 invariant: N
// requests racing on one stale OAuth credential must trigger exactly one
// token refresh. refreshIfStale/credLock (gateway.go:256-290) serialize
// refreshes per credID and re-read the credential under the lock so
// 2nd..Nth callers see the freshened token and skip their own refresh.
func TestGateway_ConcurrentRefreshCollapsesToOne(t *testing.T) {
	up, _ := upstreamRecording(t)
	cred := Credential{Mode: ModeOAuthBearer, Secret: "stale", RefreshToken: "r1",
		ExpiresAt: time.Now().Add(-time.Minute), BaseURL: up.URL, TokenEndpoint: "http://unused"}
	g, handle := gatewayWith(t, cred)

	var n int64
	g.refresh = func(_ context.Context, c Credential) (Credential, error) {
		atomic.AddInt64(&n, 1)
		c.Secret = "fresh"
		c.ExpiresAt = time.Now().Add(time.Hour) // future: under-lock re-read sees this as fresh
		return c, nil
	}
	gw := serve(t, g)

	const concurrency = 20
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPost, gw.URL+"/mcp", nil)
			if err != nil {
				t.Errorf("new request: %v", err)
				return
			}
			req.Header.Set("Authorization", "Bearer "+handle)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("do: %v", err)
				return
			}
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.StatusCode)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&n); got != 1 {
		t.Errorf("refresh called %d times, want exactly 1", got)
	}
}

func TestGateway_ExpiredHandle401(t *testing.T) {
	up, _ := upstreamRecording(t)
	v := NewVault()
	h := NewHandles(v)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	id, _ := v.Store("local", Credential{Mode: ModeSubscription, Secret: "x", BaseURL: up.URL})
	handle, _ := h.IssueHandle("local", id, "box", time.Hour)
	now = now.Add(2 * time.Hour) // advance past expiry
	gw := serve(t, NewGateway(h))

	if code := do(t, gw, handle.Value, http.MethodGet, "/x", nil); code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", code)
	}
}

// revocableBearer starts a fake OAuth-protected MCP upstream that 401s (with a
// Bearer resource_metadata= challenge) every request whose bearer isn't okToken,
// and 200s the rest. It records every bearer it saw, in order. okToken == "" is
// the "reject everything" upstream (used by the fails-closed test).
func revocableBearer(t *testing.T, okToken string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		seen = append(seen, auth)
		mu.Unlock()
		if okToken == "" || auth != "Bearer "+okToken {
			// The upstream OAuth challenge the pod must NEVER see.
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://mcp.example/.well-known/oauth-protected-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("unauthorized"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func hasKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// TestGateway_ReactiveRetryOnUpstream401 covers §4: an upstream 401 on a
// not-yet-stale token (early revocation inside the refresh skew) triggers
// exactly one force-refresh + replay, the retry carries the refreshed bearer,
// the client gets the 200, and never sees an upstream OAuth challenge.
func TestGateway_ReactiveRetryOnUpstream401(t *testing.T) {
	up, seen := revocableBearer(t, "fresh")
	// Far-future expiry => NOT stale => no proactive refresh; the refresh only
	// fires reactively, off the upstream 401.
	cred := Credential{Mode: ModeOAuthBearer, Secret: "access-old", RefreshToken: "r1",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: up.URL, TokenEndpoint: "http://unused", WriteBackKey: "gh"}
	g, handle := gatewayWith(t, cred)

	var refreshes int64
	g.refresh = func(_ context.Context, c Credential) (Credential, error) {
		atomic.AddInt64(&refreshes, 1)
		c.Secret = "fresh"
		c.ExpiresAt = time.Now().Add(time.Hour)
		return c, nil
	}
	gw := serve(t, g)

	resp := doResp(t, gw, handle, http.MethodPost, "/mcp")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (reactive retry should succeed)", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != "" {
		t.Errorf("client saw WWW-Authenticate = %q, want it stripped", got)
	}
	if n := atomic.LoadInt64(&refreshes); n != 1 {
		t.Errorf("refresh called %d times, want exactly 1", n)
	}
	got := seen()
	if len(got) != 2 {
		t.Fatalf("upstream saw %d requests, want 2 (original + one retry): %v", len(got), got)
	}
	if got[0] != "Bearer access-old" {
		t.Errorf("upstream first bearer = %q, want %q", got[0], "Bearer access-old")
	}
	if got[1] != "Bearer fresh" {
		t.Errorf("upstream retry bearer = %q, want %q (refreshed)", got[1], "Bearer fresh")
	}
	if r := g.NeedsReauth(); len(r) != 0 {
		t.Errorf("NeedsReauth() = %v, want empty after a successful retry", r)
	}
}

// TestGateway_ReactiveRetryStill401FailsClosed covers §4: when even the
// refreshed token 401s, the grant is dead — the client gets a bare 401 (NO
// upstream challenge) and the connection is flagged NeedsReauth.
func TestGateway_ReactiveRetryStill401FailsClosed(t *testing.T) {
	up, seen := revocableBearer(t, "") // reject everything, incl. the refreshed token
	cred := Credential{Mode: ModeOAuthBearer, Secret: "access-old", RefreshToken: "r1",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: up.URL, TokenEndpoint: "http://unused", WriteBackKey: "gh"}
	g, handle := gatewayWith(t, cred)

	var refreshes int64
	g.refresh = func(_ context.Context, c Credential) (Credential, error) {
		atomic.AddInt64(&refreshes, 1)
		c.Secret = "fresh"
		c.ExpiresAt = time.Now().Add(time.Hour)
		return c, nil
	}
	gw := serve(t, g)

	resp := doResp(t, gw, handle, http.MethodPost, "/mcp")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != "" {
		t.Errorf("client saw WWW-Authenticate = %q, want it stripped even on the failing retry", got)
	}
	if n := atomic.LoadInt64(&refreshes); n != 1 {
		t.Errorf("refresh called %d times, want exactly 1 (retry at most once)", n)
	}
	if got := seen(); len(got) != 2 {
		t.Errorf("upstream saw %d requests, want 2 (original + one retry): %v", len(got), got)
	}
	if r := g.NeedsReauth(); !hasKey(r, "gh") {
		t.Errorf("NeedsReauth() = %v, want it to contain %q (grant is dead)", r, "gh")
	}
}

// TestGateway_StripsUpstreamWWWAuthenticate covers §4: even on a 200, an
// upstream WWW-Authenticate must be stripped before the pod's MCP client can
// see it (it would otherwise start its own OAuth against the broker).
func TestGateway_StripsUpstreamWWWAuthenticate(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://mcp.example/x"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(up.Close)
	cred := Credential{Mode: ModeOAuthBearer, Secret: "access", ExpiresAt: time.Now().Add(time.Hour), BaseURL: up.URL}
	g, handle := gatewayWith(t, cred)
	gw := serve(t, g)

	resp := doResp(t, gw, handle, http.MethodPost, "/mcp")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != "" {
		t.Errorf("client saw WWW-Authenticate = %q, want it stripped", got)
	}
}

// TestGateway_NonOAuth401NotRetriedNorStripped covers §4's scope guard: only
// ModeOAuthBearer upstreams get the retry + strip. A non-OAuth upstream's 401 —
// and its WWW-Authenticate — pass through byte-for-byte, and no refresh fires.
func TestGateway_NonOAuth401NotRetriedNorStripped(t *testing.T) {
	var hits int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("WWW-Authenticate", `Basic realm="vendor"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(up.Close)
	// ModeBasic secret is "user:token".
	cred := Credential{Mode: ModeBasic, Vendor: "forgejo", Secret: "realuser:realtoken", BaseURL: up.URL}
	g, handle := gatewayWith(t, cred)
	g.refresh = func(context.Context, Credential) (Credential, error) {
		t.Error("refresh must NOT be called for a non-OAuth upstream")
		return Credential{}, errors.New("unexpected refresh")
	}
	gw := serve(t, g)

	resp := doResp(t, gw, handle, http.MethodGet, "/x")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (passthrough)", resp.StatusCode)
	}
	if got, want := resp.Header.Get("WWW-Authenticate"), `Basic realm="vendor"`; got != want {
		t.Errorf("WWW-Authenticate = %q, want the upstream challenge passed through unchanged (%q)", got, want)
	}
	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Errorf("upstream hit %d times, want 1 (no retry for a non-OAuth upstream)", n)
	}
}
