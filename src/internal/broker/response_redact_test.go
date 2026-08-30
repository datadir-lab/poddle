package broker

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests cover the secretless-invariant audit fix I-1: a credential's
// configured upstream that REFLECTS the injected credential back in its response
// (a debug/echo route, a verbose error quoting Authorization, a whoami reply, an
// MCP tool mirroring input) must not hand a hostile pod its own real secret. The
// gateway scrubs the injected secret out of a bounded, textual, non-streaming
// response before it reaches the pod.

// upstreamReflecting starts a fake vendor that ECHOES the credential headers it
// received back in a JSON response body, modeling a reflection surface. It sets
// an explicit Content-Length so the response is a bounded, textual body (the
// case the scrub covers).
func upstreamReflecting(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		echo := fmt.Sprintf(`{"authorization":%q,"x-api-key":%q,"x-goog-api-key":%q}`,
			r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"), r.Header.Get("X-Goog-Api-Key"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(echo)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(echo))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// readBody drives a request through the gateway and returns status + response body.
func readBody(t *testing.T, gw *httptest.Server, handle, method, target string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, gw.URL+target, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+handle)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err) // a wrong Content-Length would surface here (truncation/hang)
	}
	return resp.StatusCode, string(b)
}

func TestGateway_ScrubsReflectedSecretInResponse_Subscription(t *testing.T) {
	up := upstreamReflecting(t)
	const secret = "sk-REALSECRET-must-not-return"
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: secret, BaseURL: up.URL})
	gw := serve(t, g)

	code, body := readBody(t, gw, handle, http.MethodPost, "/echo")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if strings.Contains(body, secret) {
		t.Errorf("reflected secret reached the pod: %q", body)
	}
	if !strings.Contains(body, RedactPlaceholder) {
		t.Errorf("expected a redaction placeholder in the scrubbed response, got %q", body)
	}
}

func TestGateway_ScrubsReflectedSecretInResponse_APIKey(t *testing.T) {
	up := upstreamReflecting(t)
	const secret = "apikey-REALSECRET-must-not-return"
	g, handle := gatewayWith(t, Credential{Mode: ModeAPIKey, Secret: secret, BaseURL: up.URL})
	gw := serve(t, g)

	code, body := readBody(t, gw, handle, http.MethodPost, "/echo")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if strings.Contains(body, secret) {
		t.Errorf("reflected X-Api-Key reached the pod: %q", body)
	}
}

func TestGateway_ScrubsReflectedSecretInResponse_OAuth(t *testing.T) {
	up := upstreamReflecting(t)
	const secret = "oauth-access-REALSECRET-must-not-return"
	// Far-future expiry => not stale => no proactive refresh; the upstream 200s so
	// no reactive retry — the scrub is the only thing acting on the response.
	cred := Credential{Mode: ModeOAuthBearer, Secret: secret, ExpiresAt: time.Now().Add(time.Hour), BaseURL: up.URL}
	g, handle := gatewayWith(t, cred)
	gw := serve(t, g)

	code, body := readBody(t, gw, handle, http.MethodPost, "/mcp")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if strings.Contains(body, secret) {
		t.Errorf("reflected OAuth access token reached the pod: %q", body)
	}
}

// TestGateway_ScrubsReflectedSecretInResponse_Gzip is the regression guard for
// audit C1: a gzip'd reflected response must NOT leak. The Director strips the
// outbound Accept-Encoding so Go's transport transparently decodes gzip, and the
// scrub then operates on plaintext — the pod receives a scrubbed, decompressed body.
func TestGateway_ScrubsReflectedSecretInResponse_Gzip(t *testing.T) {
	const secret = "sk-REALSECRET-gzip-must-not-return"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		echo := fmt.Sprintf(`{"authorization":%q}`, r.Header.Get("Authorization"))
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		_, _ = gz.Write([]byte(echo))
		_ = gz.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(b.Len()))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b.Bytes())
	}))
	t.Cleanup(up.Close)
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: secret, BaseURL: up.URL})
	gw := serve(t, g)

	code, body := readBody(t, gw, handle, http.MethodPost, "/echo")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if strings.Contains(body, secret) {
		t.Errorf("gzip'd reflected secret reached the pod: %q", body)
	}
	if !strings.Contains(body, RedactPlaceholder) {
		t.Errorf("expected placeholder in the scrubbed (decompressed) response, got %q", body)
	}
}

// TestGateway_ScrubsReflectedSecretInResponse_OAuthRetryGzip is the regression
// guard for round-3 audit CRITICAL: the OAuth reactive-retry splice must carry
// res2.Uncompressed, or a gzip'd retry-200 that reflects the REFRESHED token —
// decompressed by the transport (Uncompressed, ContentLength -1) — would be
// mistaken for a chunked stream and forwarded UNSCANNED. Early-revocation 401 on
// the first token triggers one retry; the retry 200 reflects the refreshed bearer
// in a gzip'd body; the pod must receive it scrubbed.
func TestGateway_ScrubsReflectedSecretInResponse_OAuthRetryGzip(t *testing.T) {
	const refreshed = "fresh-REALSECRET-must-not-return"
	var calls int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&calls, 1) == 1 {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://mcp.example/x"`)
			w.WriteHeader(http.StatusUnauthorized) // early-revocation 401 -> triggers one retry
			return
		}
		echo := fmt.Sprintf(`{"authorization":%q}`, r.Header.Get("Authorization"))
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		_, _ = gz.Write([]byte(echo))
		_ = gz.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(b.Len()))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b.Bytes())
	}))
	t.Cleanup(up.Close)
	cred := Credential{Mode: ModeOAuthBearer, Secret: "access-old", RefreshToken: "r1",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: up.URL, TokenEndpoint: "http://unused"}
	g, handle := gatewayWith(t, cred)
	localKeeperOf(g).refresh = func(_ context.Context, c Credential) (Credential, error) {
		c.Secret = refreshed
		c.ExpiresAt = time.Now().Add(time.Hour)
		return c, nil
	}
	gw := serve(t, g)

	code, body := readBody(t, gw, handle, http.MethodPost, "/mcp")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (reactive retry should succeed)", code)
	}
	if strings.Contains(body, refreshed) {
		t.Errorf("gzip'd OAuth-retry response leaked the refreshed secret to the pod: %q", body)
	}
	if !strings.Contains(body, RedactPlaceholder) {
		t.Errorf("expected placeholder in the scrubbed retry response, got %q", body)
	}
}

// TestGateway_ScrubsReflectedSecretInResponse_ProblemJSON is the regression guard
// for audit C2: a verbose error rendered as application/problem+json (a named
// reflection surface) must be scanned via the +json structured-syntax suffix.
func TestGateway_ScrubsReflectedSecretInResponse_ProblemJSON(t *testing.T) {
	const secret = "sk-REALSECRET-problemjson-must-not-return"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		echo := fmt.Sprintf(`{"detail":"invalid token: %s"}`, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("Content-Length", strconv.Itoa(len(echo)))
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(echo))
	}))
	t.Cleanup(up.Close)
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: secret, BaseURL: up.URL})
	gw := serve(t, g)

	code, body := readBody(t, gw, handle, http.MethodGet, "/x")
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
	if strings.Contains(body, secret) {
		t.Errorf("reflected secret in problem+json reached the pod: %q", body)
	}
}

// TestGateway_ResponseScrub_FailsClosedOnUnscannableEncoding: a textual response
// arriving with a non-identity Content-Encoding the transport did not decode
// (a non-compliant upstream using an encoding the broker never advertised) is
// ciphertext the exact-match scan can't cover — so the gateway fails closed (502)
// rather than forward a body it can't verify is secret-free.
func TestGateway_ResponseScrub_FailsClosedOnUnscannableEncoding(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "br") // never advertised by the broker; transport won't decode
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte{0, 1, 2, 3, 4})
	}))
	t.Cleanup(up.Close)
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "sk-secret", BaseURL: up.URL})
	gw := serve(t, g)

	code, _ := readBody(t, gw, handle, http.MethodGet, "/x")
	if code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (fail closed on unscannable Content-Encoding)", code)
	}
}

// TestGateway_ResponseScrub_FailsClosedOnTruncatedBody is the regression guard
// for round-2 audit C-1: if the body read errors (the upstream reflects the
// secret in a prefix then drops the connection short of its declared
// Content-Length), the partial body is UNVERIFIED, so the gateway must fail
// closed (drop it) rather than forward the partial unscanned.
func TestGateway_ResponseScrub_FailsClosedOnTruncatedBody(t *testing.T) {
	const secret = "sk-REALSECRET-truncated-must-not-return"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("upstream ResponseWriter is not a Hijacker")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		// Reflect the secret in a body PREFIX, declare a much larger Content-Length,
		// then close early -> the broker reads the secret bytes then an unexpected EOF.
		prefix := fmt.Sprintf(`{"authorization":"%s"`, r.Header.Get("Authorization"))
		fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 100000\r\n\r\n%s", prefix)
	}))
	t.Cleanup(up.Close)
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: secret, BaseURL: up.URL})
	gw := serve(t, g)

	code, body := readBody(t, gw, handle, http.MethodGet, "/echo")
	if strings.Contains(body, secret) {
		t.Errorf("truncated reflected body leaked the secret to the pod (status %d): %q", code, body)
	}
	if code == http.StatusOK {
		t.Errorf("a truncated, unverifiable response must not be delivered as 200 (got body %q)", body)
	}
}

// TestGateway_ResponseScrub_ForwardsChunkedTextualStream is the regression guard
// for round-2 audit I-2: a genuinely chunked identity textual stream (no
// Content-Length, not transport-decompressed) must be forwarded unscanned rather
// than buffered, so streaming textual responses (e.g. Gemini
// streamGenerateContent without alt=sse) keep incremental delivery. Body intact.
func TestGateway_ResponseScrub_ForwardsChunkedTextualStream(t *testing.T) {
	const part1, part2 = `[{"chunk":1}`, `,{"chunk":2}]`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, part1)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush() // flush before EOF -> chunked (no Content-Length)
		}
		_, _ = io.WriteString(w, part2)
	}))
	t.Cleanup(up.Close)
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "sk-secret", BaseURL: up.URL})
	gw := serve(t, g)

	code, body := readBody(t, gw, handle, http.MethodGet, "/stream")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body != part1+part2 {
		t.Errorf("chunked textual stream altered:\n got %q\nwant %q", body, part1+part2)
	}
}

// TestGateway_ResponseScrub_PreservesNonReflectingBody is the regression guard:
// a normal response that does NOT contain the secret must pass through byte-for-
// byte (the hits==0 path restores the original body and leaves Content-Length).
func TestGateway_ResponseScrub_PreservesNonReflectingBody(t *testing.T) {
	const payload = `{"message":"hello, world","ok":true}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(up.Close)
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "sk-secret", BaseURL: up.URL})
	gw := serve(t, g)

	code, body := readBody(t, gw, handle, http.MethodGet, "/x")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body != payload {
		t.Errorf("non-reflecting body altered:\n got %q\nwant %q", body, payload)
	}
}

// TestGateway_ResponseScrub_SkipsEventStream proves an SSE (text/event-stream)
// response is forwarded untouched — the scrub gate must NOT buffer it, or LLM
// token streaming would break. The body arrives intact.
func TestGateway_ResponseScrub_SkipsEventStream(t *testing.T) {
	const stream = "data: chunk-one\n\ndata: chunk-two\n\n"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(stream))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(up.Close)
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "sk-secret", BaseURL: up.URL})
	gw := serve(t, g)

	code, body := readBody(t, gw, handle, http.MethodGet, "/stream")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body != stream {
		t.Errorf("SSE stream altered:\n got %q\nwant %q", body, stream)
	}
}

// --- keeper-level unit tests for RedactResponse (managed-secret exact match) ---

func TestKeeper_RedactResponse_ScrubsManagedSecret(t *testing.T) {
	const secret = "sk-managed-secret-token"
	k, handle, _ := keeperWith(t, Credential{Mode: ModeSubscription, Secret: secret, BaseURL: "https://x"})
	body := []byte(`{"echo":"Bearer ` + secret + `"}`)
	scrubbed, hits, err := k.RedactResponse(handle, body)
	if err != nil {
		t.Fatalf("RedactResponse: %v", err)
	}
	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
	if strings.Contains(string(scrubbed), secret) {
		t.Errorf("secret not scrubbed from response: %q", scrubbed)
	}
	if !strings.Contains(string(scrubbed), RedactPlaceholder) {
		t.Errorf("expected placeholder, got %q", scrubbed)
	}
}

func TestKeeper_RedactResponse_BasicScrubsTokenHalf(t *testing.T) {
	// ModeBasic secret is "user:token"; a reflected body may echo just the token,
	// so the token half must be scrubbed too (the managedSecrets strings.Cut branch).
	const token = "ghp_reflectedtoken0000000000000000000000"
	k, handle, _ := keeperWith(t, Credential{Mode: ModeBasic, Secret: "octocat:" + token, BaseURL: "http://x"})
	body := []byte(`{"leaked":"` + token + `"}`)
	scrubbed, hits, err := k.RedactResponse(handle, body)
	if err != nil {
		t.Fatalf("RedactResponse: %v", err)
	}
	if hits == 0 || strings.Contains(string(scrubbed), token) {
		t.Errorf("token half not scrubbed: hits=%d scrubbed=%q", hits, scrubbed)
	}
}

func TestKeeper_RedactResponse_NoMatchUntouched(t *testing.T) {
	k, handle, _ := keeperWith(t, Credential{Mode: ModeSubscription, Secret: "sk-secret", BaseURL: "https://x"})
	body := []byte(`{"message":"nothing sensitive here"}`)
	scrubbed, hits, err := k.RedactResponse(handle, body)
	if err != nil {
		t.Fatalf("RedactResponse: %v", err)
	}
	if hits != 0 || string(scrubbed) != string(body) {
		t.Errorf("clean body should be untouched: hits=%d scrubbed=%q", hits, scrubbed)
	}
}

// TestKeeper_RedactResponse_ResolveErrorFailsClosed guards audit I1: a resolve
// failure (handle revoked/expired mid-request) must FAIL CLOSED — return an error
// so the front drops the response — not forward the reflected body unscrubbed.
func TestKeeper_RedactResponse_ResolveErrorFailsClosed(t *testing.T) {
	k, _, _ := keeperWith(t, Credential{Mode: ModeSubscription, Secret: "s", BaseURL: "https://x"})
	if _, _, err := k.RedactResponse("no-such-handle", []byte(`{"hello":"world"}`)); err == nil {
		t.Error("resolve-fail must fail closed (return an error), got nil")
	}
}

// --- two-process RPC round-trip for RedactResponse ---

func TestKeeperRPC_RedactResponse(t *testing.T) {
	const secret = "sk-rpc-managed-secret"
	c, _, handle, _ := rpcPair(t, Credential{Mode: ModeSubscription, Secret: secret, BaseURL: "https://x"})
	body := []byte(`{"echo":"Bearer ` + secret + `"}`)
	scrubbed, hits, err := c.RedactResponse(handle, body)
	if err != nil {
		t.Fatalf("RedactResponse over RPC: %v", err)
	}
	if hits != 1 || strings.Contains(string(scrubbed), secret) {
		t.Errorf("RPC scrub failed: hits=%d scrubbed=%q", hits, scrubbed)
	}
}

// TestKeeperRPC_RedactResponse_FailsClosedOnDeadKeeper proves the fail-closed
// contract: when the keeper conn is closed, RedactResponse returns an error (so
// the gateway drops the response) rather than forwarding an unscrubbed body.
func TestKeeperRPC_RedactResponse_FailsClosedOnDeadKeeper(t *testing.T) {
	c, _, handle, _ := rpcPair(t, Credential{Mode: ModeSubscription, Secret: "sk-secret", BaseURL: "https://x"})
	_ = c.Close() // kill the keeper conn
	if _, _, err := c.RedactResponse(handle, []byte(`{"x":1}`)); err == nil {
		t.Error("RedactResponse against a dead keeper must error (fail closed), got nil")
	}
}
