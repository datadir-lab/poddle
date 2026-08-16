package broker

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// recordedReq captures what the upstream (fake vendor) actually received.
type recordedReq struct {
	method, path, query, auth, apikey, body string
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
