package broker

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

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

// Gateway is the secretless egress proxy FRONT. A pod's harness points at it
// (BASE_URL) and presents a handle (in Authorization); the gateway asks its
// Keeper to resolve the handle, inject the REAL secret per the credential's mode,
// and redact secrets from outbound bodies — then reverse-proxies to the vendor.
//
// The front parses the untrusted request and drives the reverse proxy but NEVER
// holds a plaintext Credential: it deals only in a credID, a non-secret
// PublicCred, and an opaque fingerprint of the injected token. Every
// secret-touching step is delegated to g.keeper (the Tier-2 privilege boundary;
// see docs/design/broker-privilege-separation.md). It also reports every request
// to an Auditor.
type Gateway struct {
	keeper       Keeper
	auditor      Auditor
	policy       PolicyChecker
	loopbackHost string // if set, a loopback upstream is dialed here (the host); see RewriteLoopbackHost
}

// NewGateway returns a gateway backed by the handle registry, with an in-process
// keeper that redacts egress by default (mode "redact") and installs the default
// OAuth refresh function.
func NewGateway(h *Handles) *Gateway {
	return &Gateway{keeper: newLocalKeeper(h)}
}

// SetEgressMode configures egress redaction: "redact" (default), "block", "off".
func (g *Gateway) SetEgressMode(mode string) { g.keeper.SetEgressMode(mode) }

// SetAuditor sets the sink that receives one record per proxied request.
func (g *Gateway) SetAuditor(a Auditor) { g.auditor = a }

// SetPolicyChecker sets the governance policy checker consulted per request.
func (g *Gateway) SetPolicyChecker(pc PolicyChecker) { g.policy = pc }

// SetLoopbackHost makes a loopback upstream (e.g. a local Postgres at
// 127.0.0.1) dial loopbackHost instead — the host route from a containerized
// broker (host.containers.internal). Empty (the default) disables the rewrite.
func (g *Gateway) SetLoopbackHost(h string) { g.loopbackHost = h }

// SetOAuthPersister wires the sink that durably mirrors a connection's rotated
// OAuth refresh token to disk, so it survives a poddled restart. Nil (the
// default) disables write-back — a refresh still rotates the in-memory
// credential, it just isn't mirrored.
func (g *Gateway) SetOAuthPersister(p OAuthPersister) { g.keeper.SetOAuthPersister(p) }

// NeedsReauth returns the WriteBackKeys of connections whose most recent OAuth
// refresh attempt failed, sorted for stable output. A key clears once the host
// re-stores a fresh credential for it (Broker.Store).
func (g *Gateway) NeedsReauth() []string { return g.keeper.NeedsReauth() }

// clearReauth clears the needs-reauth flag for key, if any is set (delegated to
// the keeper). Called by the facade when the host re-stores a credential.
func (g *Gateway) clearReauth(key string) { g.keeper.ClearReauth(key) }

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
	credID, pub, err := g.keeper.Resolve(handle)
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
	up, err := url.Parse(pub.BaseURL)
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
	// refresh token). This is a front-side check — it needs only the public
	// hostname, no secret.
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

	// Keeper-side: refresh a stale OAuth access token and inject the REAL secret
	// into the request headers, returning only a non-secret fingerprint. Injecting
	// here — BEFORE building/serving the proxy — preserves the fail-closed timing
	// of the former (pre-proxy refreshIfStale + in-Director applyAuth): a refresh
	// failure aborts with a BARE 401 (no WWW-Authenticate, so it can't be mistaken
	// for an OAuth challenge) before any byte reaches the upstream. The injected
	// auth rides r.Header, which the reverse proxy clones into the outbound
	// request. Audited (secret-free) so operators see the credential needs
	// `connect reauth`.
	mut, fp, err := g.keeper.InjectAuth(r.Context(), handle, credID)
	if err != nil {
		g.audit(handle, up.Host, method, path, "deny", "oauth refresh failed — needs reauth", http.StatusUnauthorized)
		http.Error(w, "poddle: MCP upstream authorization failed", http.StatusUnauthorized)
		return
	}
	mut.Apply(r.Header)

	// Egress redaction: the front applies the non-secret gate (body present +
	// textual Content-Type + within scan size), reads the bytes, and hands them to
	// the keeper — which holds the managed secret and returns the scrubbed body (or
	// a block decision). Git and other binary payloads are skipped so packfiles
	// aren't buffered/mangled. Runs AFTER InjectAuth so redaction targets the
	// post-rotation secret already in the vault.
	hits := 0
	if r.Body != nil && isTextual(r.Header.Get("Content-Type")) && r.ContentLength <= maxScanBytes {
		body, rerr := io.ReadAll(io.LimitReader(r.Body, maxScanBytes))
		_ = r.Body.Close()
		if rerr != nil {
			r.Body = io.NopCloser(bytes.NewReader(body)) // restore partial; skip scan
		} else {
			scrubbed, blocked, n := g.keeper.RedactBody(handle, body)
			if blocked {
				g.audit(handle, up.Host, method, path, "block", "egress blocked — secret detected", http.StatusForbidden)
				http.Error(w, "poddle: outbound request blocked — secret detected", http.StatusForbidden)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(scrubbed))
			r.ContentLength = int64(len(scrubbed))
			r.Header.Set("Content-Length", strconv.Itoa(len(scrubbed)))
			hits = n
		}
	}

	// For an OAuth upstream, buffer the request body so the reactive-retry path
	// (ModifyResponse below) can replay it under a freshly refreshed token. MCP
	// JSON-RPC requests are small; this mirrors the redact-body buffering above.
	// ONLY OAuth creds are buffered — git and other upstreams stream unchanged so
	// large packfiles aren't held in memory. pub.Mode is non-secret.
	var bodyBytes []byte
	if pub.Mode == ModeOAuthBearer && r.Body != nil {
		b, rerr := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if rerr != nil {
			http.Error(w, "poddle: MCP upstream request read failed", http.StatusBadGateway)
			return
		}
		bodyBytes = b
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		r.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		}
	}

	proxy := httputil.NewSingleHostReverseProxy(g.dialURL(up))
	base := proxy.Director
	proxy.Director = func(req *http.Request) {
		base(req)          // scheme/host/path -> upstream (loopback dialed at the host)
		req.Host = up.Host // match the REAL upstream Host header, not the dial host
		// The real auth was injected into r.Header by InjectAuth above and cloned
		// into req by the reverse proxy — no secret is applied here on the front.
	}
	// OAuth upstreams only (§4): the pod's MCP client must NEVER see an upstream
	// WWW-Authenticate — a `Bearer resource_metadata=` challenge would send it off
	// to run its own OAuth handshake against the broker — so strip it always. And
	// on a 401 against a token that wasn't yet stale (an early revocation inside
	// the refresh skew, so no proactive refresh ran), force exactly one refresh
	// and replay the request before surfacing the failure. Non-OAuth upstreams are
	// left byte-for-byte unchanged (no ModifyResponse, no buffering).
	if pub.Mode == ModeOAuthBearer {
		retried := false
		proxy.ModifyResponse = func(res *http.Response) error {
			res.Header.Del("WWW-Authenticate")
			if res.StatusCode != http.StatusUnauthorized || retried {
				return nil
			}
			retried = true // retry AT MOST once per request
			// res.Request is the outbound request the Director built (URL already
			// the dial target, Host == up.Host, carrying the injected bearer the
			// upstream just rejected); clone it and reset the body from the replay
			// buffer.
			req2 := res.Request.Clone(res.Request.Context())
			if r.GetBody != nil {
				rc, gerr := r.GetBody()
				if gerr != nil {
					return nil // keep the stripped 401
				}
				req2.Body = rc
			} else {
				req2.Body = nil
			}
			req2.ContentLength = int64(len(bodyBytes))
			// Reactive re-inject under a force-refreshed (or peer-refreshed) token,
			// keyed on the fingerprint the front holds — never the token itself. On
			// failure the keeper already flagged needs-reauth; leave the (already
			// stripped) bare 401 for the pod.
			reMut, rerr := g.keeper.ForceReinject(res.Request.Context(), handle, credID, fp)
			if rerr != nil {
				return nil
			}
			reMut.Apply(req2.Header)
			res2, rterr := http.DefaultTransport.RoundTrip(req2)
			if rterr != nil {
				return nil // retry transport error: keep the stripped 401
			}
			_ = res.Body.Close()
			res.StatusCode = res2.StatusCode
			res.Status = res2.Status
			res.Header = res2.Header
			res.Body = res2.Body
			res.ContentLength = res2.ContentLength
			// The stdlib's reverse proxy runs removeHopByHopHeaders on the ORIGINAL
			// response before ModifyResponse, but res2 (the retry response) never went
			// through that — strip it here too, or a retry upstream's
			// Connection/Keep-Alive/etc. reach the pod.
			stripHopByHop(res.Header)
			res.Header.Del("WWW-Authenticate") // the retry response may carry one too
			if res2.StatusCode == http.StatusUnauthorized {
				g.keeper.FlagReauth(handle) // even the refreshed token was rejected — the grant is dead
			}
			return nil
		}
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

// hopByHopHeaders are the standard connection-scoped headers (RFC 9110
// §7.6.1) that must never be forwarded end-to-end by a proxy.
var hopByHopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

// stripHopByHop deletes the standard hop-by-hop headers from h, plus any
// extra header named in h's own Connection header value (RFC 9110 §7.6.1: a
// Connection header can nominate additional per-hop headers beyond the
// standard set). net/http/httputil's ReverseProxy does this for the FIRST
// upstream response automatically before ModifyResponse runs; it does nothing
// for a second response the reactive-retry path splices in, so callers that
// swap in a retry response's header wholesale must call this themselves.
func stripHopByHop(h http.Header) {
	if conn := h.Get("Connection"); conn != "" {
		for _, name := range strings.Split(conn, ",") {
			h.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range hopByHopHeaders {
		h.Del(name)
	}
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
