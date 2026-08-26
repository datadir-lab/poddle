package broker

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/datadir-lab/poddle/src/internal/oauth"
)

// refreshSkew is how far before an OAuth access token's ExpiresAt the gateway
// proactively refreshes it, so an in-flight request isn't injected with a token
// that expires mid-flight.
const refreshSkew = 60 * time.Second

// maxScanBytes bounds egress redaction; larger bodies are forwarded unscanned.
const maxScanBytes = 25 << 20

// ProxyRecord is what the gateway reports to an Auditor for each request it
// handles. It is secret-free: Path has no query-string and no body is included.
// Pod resolution (handle → pod) is the Auditor's job (the daemon owns it).
type ProxyRecord struct {
	Handle   string // the pod-presented handle; the auditor maps it to a pod
	Upstream string // destination host
	Method   string
	Path     string // no query-string
	Decision string // allow | redact | block
	Detail   string // e.g. "redacted 2 secrets"; never a secret
	Status   int
}

// Auditor receives one record per proxied request. The daemon implements it.
type Auditor interface{ Proxy(ProxyRecord) }

// PolicyChecker decides whether the pod holding handle may reach host with
// method. The daemon implements it (handle -> pod -> policy); nil = allow all.
type PolicyChecker interface {
	Check(handle, host, method string) (allow bool, reason string)
}

// MonitorChecker is an optional companion to PolicyChecker: when the pod's
// policy is in monitor mode, a would-be denial is forwarded (not blocked) and
// recorded as "monitor" so operators can roll a policy out safely before
// enforcing it. A checker that does not implement it always enforces.
type MonitorChecker interface {
	Monitored(handle string) bool
}

// InterceptChecker is an optional companion to PolicyChecker: when the pod's
// policy opts into interception, the forward proxy terminates TLS on its HTTPS
// egress (rather than tunnelling opaquely) so per-request method rules apply.
type InterceptChecker interface {
	Intercepts(handle, host string) bool
}

// EgressModer is an optional companion to PolicyChecker: it reports a pod's
// egress redaction mode ("redact" | "block" | "off") by handle, so intercepted
// HTTPS request bodies honour the same egress mode as the brokered path. A
// checker that does not implement it defaults to "redact".
type EgressModer interface {
	EgressMode(handle string) string
}

// LeafSource mints a TLS leaf certificate for host, signed by the egress CA the
// intercepted pod trusts. *tlsca.Authority satisfies it; kept as an interface so
// the broker does not depend on the CA implementation.
type LeafSource interface {
	LeafFor(host string) (*tls.Certificate, error)
}

// Gateway is the secretless egress proxy. A pod's harness points at it
// (BASE_URL) and presents a handle (in Authorization); the gateway resolves the
// handle to a Credential, injects the REAL secret per the credential's mode, and
// reverse-proxies to the vendor. The real secret never leaves the broker. It
// also redacts secrets from outbound bodies (egress DLP) and reports every
// request to an Auditor.
type Gateway struct {
	handles      *Handles
	redactor     *Redactor
	auditor      Auditor
	policy       PolicyChecker
	loopbackHost string // if set, a loopback upstream is dialed here (the host); see RewriteLoopbackHost

	// refresh mints a fresh Credential from a stale ModeOAuthBearer one (default
	// calls oauth.Refresh; tests override it). refMu guards refLocks, a per-credID
	// mutex map that serializes concurrent refreshes of one credential so N racing
	// requests trigger exactly one token request.
	refresh  func(context.Context, Credential) (Credential, error)
	refMu    sync.Mutex
	refLocks map[string]*sync.Mutex
}

// NewGateway returns a gateway backed by the handle registry, redacting egress
// by default (mode "redact"). It installs the default OAuth refresh function,
// which calls the credential's token endpoint and rebuilds the Credential with
// the new access/refresh token and expiry (all other fields preserved).
func NewGateway(h *Handles) *Gateway {
	g := &Gateway{
		handles:  h,
		redactor: NewRedactor("redact"),
		refLocks: map[string]*sync.Mutex{},
	}
	g.refresh = func(ctx context.Context, cred Credential) (Credential, error) {
		tok, err := oauth.Refresh(ctx, http.DefaultClient, cred.TokenEndpoint, cred.RefreshToken, cred.ClientID, cred.ClientSecret)
		if err != nil {
			return Credential{}, err
		}
		// Copy the pre-refresh credential and overwrite ONLY the rotating fields;
		// Mode/Vendor/BaseURL/TokenEndpoint/ClientID/ClientSecret must survive the
		// vault swap (Replace takes a full Credential, not a delta).
		updated := cred
		updated.Secret = tok.AccessToken
		updated.RefreshToken = tok.RefreshToken
		updated.ExpiresAt = tok.ExpiresAt
		return updated, nil
	}
	return g
}

// SetEgressMode configures egress redaction: "redact" (default), "block", "off".
func (g *Gateway) SetEgressMode(mode string) { g.redactor = NewRedactor(mode) }

// SetAuditor sets the sink that receives one record per proxied request.
func (g *Gateway) SetAuditor(a Auditor) { g.auditor = a }

// SetPolicyChecker sets the governance policy checker consulted per request.
func (g *Gateway) SetPolicyChecker(pc PolicyChecker) { g.policy = pc }

// SetLoopbackHost makes a loopback upstream (e.g. a local Postgres at
// 127.0.0.1) dial loopbackHost instead — the host route from a containerized
// broker (host.containers.internal). Empty (the default) disables the rewrite.
func (g *Gateway) SetLoopbackHost(h string) { g.loopbackHost = h }

// dialURL returns the URL the gateway should dial for upstream up: up itself,
// or a copy with a loopback host rewritten to g.loopbackHost (scheme, port, and
// path preserved). The upstream Host header stays up.Host regardless, so the
// rewrite changes only where the packet goes, never what the upstream sees.
func (g *Gateway) dialURL(up *url.URL) *url.URL {
	if g.loopbackHost == "" {
		return up
	}
	rw := RewriteLoopbackHost(up.Host, g.loopbackHost)
	if rw == up.Host {
		return up
	}
	u2 := *up
	u2.Host = rw
	return &u2
}

// statusCapture wraps a ResponseWriter to remember the upstream status code for
// the audit record, passing Flush through so SSE (LLM streaming) still flushes.
type statusCapture struct {
	http.ResponseWriter
	code int
}

func (s *statusCapture) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }
func (s *statusCapture) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Capture the handle before the proxy mutates r.
	handle := handleFromAuth(r.Header.Get("Authorization"))
	credID, cred, err := g.handles.Resolve(handle)
	if err != nil {
		// Challenge with WWW-Authenticate: Basic. Git doesn't send Basic
		// credentials preemptively — it probes unauthenticated first and only
		// retries with the pod's handle as the Basic username after seeing this
		// challenge, so it must stay present or git-over-broker breaks. A Basic
		// challenge is safe here: it never triggers an MCP client's OAuth
		// handshake (that requires a Bearer resource_metadata= challenge), so
		// this can't turn into the broker impersonating an authorization server.
		w.Header().Set("WWW-Authenticate", `Basic realm="poddle"`)
		http.Error(w, "invalid or revoked handle", http.StatusUnauthorized)
		return
	}
	up, err := url.Parse(cred.BaseURL)
	if err != nil {
		http.Error(w, "bad upstream", http.StatusBadGateway)
		return
	}

	method, path := r.Method, r.URL.Path

	// Policy: the pod's governance policy may forbid this destination/method.
	// Match on the hostname (port-agnostic). In monitor mode a would-be denial is
	// let through and recorded (below) instead of blocked. Runs BEFORE the OAuth
	// refresh below so a request the policy will 403 never triggers a token
	// refresh (wasteful, and for a rotating provider needlessly rotates the
	// refresh token).
	var monitored string
	if g.policy != nil {
		if allow, reason := g.policy.Check(handle, up.Hostname(), method); !allow {
			if mc, ok := g.policy.(MonitorChecker); ok && mc.Monitored(handle) {
				monitored = reason
			} else {
				http.Error(w, "poddle: blocked by policy: "+reason, http.StatusForbidden)
				g.audit(handle, up.Host, method, path, "deny", reason, http.StatusForbidden)
				return
			}
		}
	}

	// Refresh a stale OAuth access token before injecting it. On refresh failure
	// fail closed (bare 401 — no WWW-Authenticate, so this can't be mistaken for
	// an OAuth challenge) rather than forwarding an expired credential. Audited
	// (secret-free) so operators see a signal that the credential needs
	// `connect reauth`.
	cred, err = g.refreshIfStale(r.Context(), handle, credID, cred)
	if err != nil {
		g.audit(handle, up.Host, method, path, "deny", "oauth refresh failed — needs reauth", http.StatusUnauthorized)
		http.Error(w, "poddle: MCP upstream authorization failed", http.StatusUnauthorized)
		return
	}

	// Egress redaction: scrub secrets from textual bodies (LLM/API JSON). Git
	// and other binary payloads are skipped so packfiles aren't buffered/mangled.
	proceed, hits := g.redactBody(w, r, cred)
	if !proceed {
		g.audit(handle, up.Host, method, path, "block", "egress blocked — secret detected", http.StatusForbidden)
		return // blocked (403 already written)
	}

	proxy := httputil.NewSingleHostReverseProxy(g.dialURL(up))
	base := proxy.Director
	proxy.Director = func(req *http.Request) {
		base(req)          // scheme/host/path -> upstream (loopback dialed at the host)
		req.Host = up.Host // match the REAL upstream Host header, not the dial host
		applyAuth(req.Header, cred)
	}
	sc := &statusCapture{ResponseWriter: w, code: http.StatusOK}
	proxy.ServeHTTP(sc, r)

	decision, detail := "allow", ""
	if hits > 0 {
		decision, detail = "redact", fmt.Sprintf("redacted %d secret(s)", hits)
	}
	if monitored != "" { // monitor mode: this would have been denied under enforcement
		decision, detail = "monitor", "would deny: "+monitored
	}
	g.audit(handle, up.Host, method, path, decision, detail, sc.code)
}

// refreshIfStale refreshes an OAuth credential nearing expiry and persists the
// new token in the vault (same credID) so the handle keeps resolving. Refreshes
// of one credential are serialized per credID: N racing MCP requests trigger a
// single token request. Returns the credential to inject, or an error (which the
// caller maps to a fail-closed 401). Non-OAuth credentials, and OAuth ones with
// no known expiry or still comfortably valid, are returned untouched.
func (g *Gateway) refreshIfStale(ctx context.Context, handle, credID string, cred Credential) (Credential, error) {
	if cred.Mode != ModeOAuthBearer || cred.ExpiresAt.IsZero() || time.Now().Add(refreshSkew).Before(cred.ExpiresAt) {
		return cred, nil
	}
	lk := g.credLock(credID)
	lk.Lock()
	defer lk.Unlock()
	// Re-read under the lock: another request may have just refreshed this
	// credential while we waited, in which case reuse its fresh token.
	if _, fresh, err := g.handles.Resolve(handle); err == nil &&
		!fresh.ExpiresAt.IsZero() && time.Now().Add(refreshSkew).Before(fresh.ExpiresAt) {
		return fresh, nil
	}
	updated, err := g.refresh(ctx, cred)
	if err != nil {
		return Credential{}, err
	}
	if err := g.handles.ReplaceCred(handle, updated); err != nil {
		return Credential{}, err
	}
	return updated, nil
}

// credLock returns the per-credID mutex that serializes refreshes of one
// credential, creating it on first use.
func (g *Gateway) credLock(credID string) *sync.Mutex {
	g.refMu.Lock()
	defer g.refMu.Unlock()
	lk := g.refLocks[credID]
	if lk == nil {
		lk = &sync.Mutex{}
		g.refLocks[credID] = lk
	}
	return lk
}

// audit reports a proxied request to the Auditor, if one is set.
func (g *Gateway) audit(handle, upstream, method, path, decision, detail string, status int) {
	if g.auditor == nil {
		return
	}
	g.auditor.Proxy(ProxyRecord{
		Handle: handle, Upstream: upstream, Method: method, Path: path,
		Decision: decision, Detail: detail, Status: status,
	})
}

// applyAuth clears the incoming handle and injects the real secret in the header
// the credential's mode expects.
func applyAuth(h http.Header, cred Credential) {
	h.Del("Authorization")
	h.Del("X-Api-Key")
	switch cred.Mode {
	case ModeAPIKey:
		h.Set("X-Api-Key", cred.Secret)
	case ModeGoogleAPIKey:
		// gemini-cli's SDK sends the handle in x-goog-api-key too (alongside the
		// Bearer handleFromAuth read); drop it and inject the real key there.
		h.Del("X-Goog-Api-Key")
		h.Set("X-Goog-Api-Key", cred.Secret)
	case ModeSubscription:
		h.Set("Authorization", "Bearer "+cred.Secret)
	case ModeOAuthBearer:
		h.Set("Authorization", "Bearer "+cred.Secret)
	case ModeBasic:
		// Secret is "user:token".
		h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cred.Secret)))
	case ModeEndpoint:
		if cred.Secret != "" {
			h.Set("Authorization", "Bearer "+cred.Secret)
		}
	}
}

// redactBody scrubs secrets from a textual outbound body, rewriting r in place.
// It returns proceed=false (after writing a 403) when redaction is set to block
// and a secret is found; otherwise proceed=true and the number of secrets
// redacted (0 when nothing was scanned or found).
func (g *Gateway) redactBody(w http.ResponseWriter, r *http.Request, cred Credential) (proceed bool, hits int) {
	if g.redactor == nil || r.Body == nil || !isTextual(r.Header.Get("Content-Type")) || r.ContentLength > maxScanBytes {
		return true, 0
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxScanBytes))
	_ = r.Body.Close()
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		return true, 0
	}
	managed := []string{cred.Secret}
	if _, tok, ok := strings.Cut(cred.Secret, ":"); ok {
		managed = append(managed, tok) // basic: also scrub the token half of user:token
	}
	red, n, block := g.redactor.Scan(body, managed...)
	if block {
		http.Error(w, "poddle: outbound request blocked — secret detected", http.StatusForbidden)
		return false, n
	}
	r.Body = io.NopCloser(bytes.NewReader(red))
	r.ContentLength = int64(len(red))
	r.Header.Set("Content-Length", strconv.Itoa(len(red)))
	return true, n
}

// isTextual reports whether a Content-Type carries scannable text (LLM/API
// JSON, forms, plain text). Binary payloads — notably git's x-git-* — are not
// scanned, so they are never buffered or altered.
func isTextual(ct string) bool {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	switch ct {
	case "application/json", "application/x-www-form-urlencoded":
		return true
	}
	return strings.HasPrefix(ct, "text/")
}

// handleFromAuth extracts the pod's handle from an incoming Authorization
// header: a Bearer token (LLM harnesses) or a Basic username (git).
func handleFromAuth(auth string) string {
	if v, ok := strings.CutPrefix(auth, "Bearer "); ok {
		return v
	}
	if v, ok := strings.CutPrefix(auth, "Basic "); ok {
		if dec, err := base64.StdEncoding.DecodeString(v); err == nil {
			user, _, _ := strings.Cut(string(dec), ":")
			return user
		}
	}
	return auth
}
