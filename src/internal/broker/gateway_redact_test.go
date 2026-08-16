package broker

import (
	"net/http"
	"strings"
	"testing"
)

// postThrough sends a POST with a body + content-type through the gateway.
func postThrough(t *testing.T, gwURL, handle, ctype, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", gwURL+"/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+handle)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return resp
}

func TestGateway_RedactsPatternInJSONEgress(t *testing.T) {
	upstream, rec := upstreamRecording(t)
	gw, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Vendor: "anthropic", Secret: "unused", BaseURL: upstream.URL})
	srv := serve(t, gw)
	resp := postThrough(t, srv.URL, handle, "application/json", `{"msg":"my key AKIAIOSFODNN7EXAMPLE ok"}`)
	defer resp.Body.Close()
	if strings.Contains(rec.body, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("secret reached the upstream: %q", rec.body)
	}
	if !strings.Contains(rec.body, RedactPlaceholder) {
		t.Errorf("expected a redaction placeholder, got %q", rec.body)
	}
}

func TestGateway_RedactsManagedSecretInEgress(t *testing.T) {
	upstream, rec := upstreamRecording(t)
	gw, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "sk-REALSECRET", BaseURL: upstream.URL})
	srv := serve(t, gw)
	// If a pod ever echoes the managed secret back out, it must not leak.
	resp := postThrough(t, srv.URL, handle, "application/json", `{"oops":"sk-REALSECRET"}`)
	defer resp.Body.Close()
	if strings.Contains(rec.body, "sk-REALSECRET") {
		t.Errorf("managed secret leaked upstream: %q", rec.body)
	}
}

func TestGateway_BlockModeRejects(t *testing.T) {
	upstream, rec := upstreamRecording(t)
	gw, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "unused", BaseURL: upstream.URL})
	gw.SetEgressMode("block")
	srv := serve(t, gw)
	resp := postThrough(t, srv.URL, handle, "application/json", `{"k":"AKIAIOSFODNN7EXAMPLE"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if rec.body != "" {
		t.Errorf("blocked request must not reach the upstream, got %q", rec.body)
	}
}

func TestGateway_SkipsNonTextualBody(t *testing.T) {
	upstream, rec := upstreamRecording(t)
	gw, handle := gatewayWith(t, Credential{Mode: ModeBasic, Secret: "me:tok", BaseURL: upstream.URL})
	srv := serve(t, gw)
	// A git-style binary payload must pass through untouched (never scanned).
	body := "PACK\x00AKIAIOSFODNN7EXAMPLE\x00"
	resp := postThrough(t, srv.URL, handle, "application/x-git-receive-pack-request", body)
	defer resp.Body.Close()
	if rec.body != body {
		t.Errorf("binary body should pass through unmodified;\n got %q\nwant %q", rec.body, body)
	}
}
