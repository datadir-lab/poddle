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

// NewGateway returns a gateway backed by the handle registry, with a fresh
// in-process keeper that redacts egress by default (mode "redact") and installs
// the default OAuth refresh function.
func NewGateway(h *Handles) *Gateway {
	return newGateway(newLocalKeeper(h))
}

// newGateway builds a gateway over an existing keeper, so the Broker facade can
// share ONE custody object between the gateway's request path and its own
// control-plane calls (in-process a *localKeeper; in two-process mode the
// socketKeeperClient). The keeper is the Tier-2 privilege boundary.
func newGateway(k Keeper) *Gateway {
	return &Gateway{keeper: k}
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
		//
		// Strip the pod-supplied Accept-Encoding so the RESPONSE reflection scrub
		// (scrubResponse, audit I-1) always sees PLAINTEXT: with no explicit
		// Accept-Encoding, Go's transport advertises only gzip and TRANSPARENTLY
		// decodes it (clearing Content-Encoding), so the upstream can't hand back a
		// compressed body that hides the injected secret from an exact-match scan.
		// The broker<->upstream wire still gzips (the transport negotiates it); only
		// the (local, bandwidth-free) broker->pod leg becomes identity.
		req.Header.Del("Accept-Encoding")
	}
	// Every response passes through ModifyResponse so the reflection scrub (audit
	// I-1) runs for ALL modes. For an OAuth upstream it ALSO strips the upstream
	// WWW-Authenticate and runs the reactive-retry FIRST, so the scrub operates on
	// the FINAL body (post-retry). Non-OAuth upstreams skip the OAuth step. The
	// scrub buffers only COMPLETE textual bodies; SSE, chunked streams, binary, and
	// oversized responses pass through untouched (see scrubResponse).
	retried := false
	proxy.ModifyResponse = func(res *http.Response) error {
		if pub.Mode == ModeOAuthBearer {
			g.oauthReactiveRetry(res, r, handle, credID, fp, bodyBytes, &retried)
		}
		return g.scrubResponse(handle, res)
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

// oauthReactiveRetry handles an OAuth upstream's response (§4). It always strips
// the upstream WWW-Authenticate (a `Bearer resource_metadata=` challenge would
// send the pod's MCP client off to run its own OAuth handshake against the
// broker), and on a 401 against a token that wasn't yet stale (an early
// revocation inside the refresh skew, so no proactive refresh ran) it force-
// refreshes under the fingerprint the front holds — never the token — and
// replays the request exactly once before surfacing the failure. It mutates res
// in place; *retried caps the replay at once per request. Every failure path
// keeps the already-stripped bare 401 (fail-closed), so it returns nothing.
// Called only for ModeOAuthBearer; non-OAuth responses are left byte-for-byte
// unchanged by the caller. This is the former in-line ModifyResponse closure,
// extracted verbatim so the reflection scrub can run for every mode after it.
func (g *Gateway) oauthReactiveRetry(res *http.Response, orig *http.Request, handle, credID, fp string, bodyBytes []byte, retried *bool) {
	res.Header.Del("WWW-Authenticate")
	if res.StatusCode != http.StatusUnauthorized || *retried {
		return
	}
	*retried = true // retry AT MOST once per request
	// res.Request is the outbound request the Director built (URL already the dial
	// target, Host == up.Host, carrying the injected bearer the upstream just
	// rejected); clone it and reset the body from the replay buffer.
	req2 := res.Request.Clone(res.Request.Context())
	if orig.GetBody != nil {
		rc, gerr := orig.GetBody()
		if gerr != nil {
			return // keep the stripped 401
		}
		req2.Body = rc
	} else {
		req2.Body = nil
	}
	req2.ContentLength = int64(len(bodyBytes))
	// Reactive re-inject under a force-refreshed (or peer-refreshed) token, keyed
	// on the fingerprint the front holds — never the token itself. On failure the
	// keeper already flagged needs-reauth; leave the (already stripped) bare 401.
	reMut, rerr := g.keeper.ForceReinject(res.Request.Context(), handle, credID, fp)
	if rerr != nil {
		return
	}
	reMut.Apply(req2.Header)
	res2, rterr := http.DefaultTransport.RoundTrip(req2)
	if rterr != nil {
		return // retry transport error: keep the stripped 401
	}
	_ = res.Body.Close()
	res.StatusCode = res2.StatusCode
	res.Status = res2.Status
	res.Header = res2.Header
	res.Body = res2.Body
	res.ContentLength = res2.ContentLength
	// Carry res2's decompression flag: req2 was cloned from the Accept-Encoding-
	// stripped outbound request, so the retry round-trip transparently gunzips too
	// (res2.Uncompressed, ContentLength -1). Without this the stale false from the
	// 401 would make scrubResponse treat the complete decompressed retry body as a
	// chunked stream and forward it UNSCANNED — leaking a reflected secret.
	res.Uncompressed = res2.Uncompressed
	// The stdlib's reverse proxy runs removeHopByHopHeaders on the ORIGINAL
	// response before ModifyResponse, but res2 (the retry response) never went
	// through that — strip it here too, or a retry upstream's
	// Connection/Keep-Alive/etc. reach the pod.
	stripHopByHop(res.Header)
	res.Header.Del("WWW-Authenticate") // the retry response may carry one too
	if res2.StatusCode == http.StatusUnauthorized {
		g.keeper.FlagReauth(handle) // even the refreshed token was rejected — the grant is dead
	}
}

// scrubResponse strips the injected secret from a reflected response body
// (secretless-invariant audit I-1): a credential's configured upstream with a
// reflection surface — a debug/echo route, a verbose error quoting the received
// Authorization, a whoami/token-introspection reply, an MCP tool that mirrors
// its input — could otherwise bounce the real credential the broker injected
// back to the pod, which controls the request path/method but never the upstream
// host. The front applies the non-secret gate (a textual, non-SSE response),
// reads the bytes, and hands them to the keeper — which holds the managed secret
// and returns the scrubbed body.
//
// The body is PLAINTEXT here: the Director stripped the outbound Accept-Encoding,
// so Go's transport advertised only gzip and transparently decoded it (clearing
// Content-Encoding). If a non-identity Content-Encoding nonetheless survived (a
// non-compliant upstream compressing with something we never advertised, e.g.
// br/zstd), the body is ciphertext we cannot exact-match — so fail CLOSED rather
// than forward it. Likewise a keeper failure (the two-process keeper is
// unreachable) fails closed. Either error makes the reverse proxy drop the
// response (502) instead of forwarding a body it could not verify is secret-free.
//
// Residual (forwarded unscanned, documented gaps — not a leak the scrub claims
// to cover): SSE (text/event-stream); ANY chunked identity response, complete OR
// streaming (ContentLength < 0 and not transport-decompressed) — this is a
// deliberate round-2 tradeoff to avoid buffering genuine streams like Gemini
// streamGenerateContent, but it also forwards a complete-but-chunked reflection
// (a dynamically generated JSON echo is commonly chunked), so it is the largest
// remaining I-1 vector; oversized bodies (> maxScanBytes); binary /
// empty-Content-Type responses; response HEADERS (audit I-3); and the base64 form
// of a reflected ModeBasic Authorization (git upstreams don't reflect). Closing
// the chunked/streaming and header vectors needs a per-flush streaming scanner +
// header scrub (tracked follow-up).
func (g *Gateway) scrubResponse(handle string, res *http.Response) error {
	if res.Body == nil {
		return nil
	}
	// A HEAD response carries no body to reflect a secret in — and its
	// Content-Encoding/Content-Length describe the would-be GET entity, so scanning
	// it would needlessly fail closed on a gzip-advertising HEAD. Skip it (body-only
	// scrub; header reflection is the documented I-3 residual either way).
	if res.Request != nil && res.Request.Method == http.MethodHead {
		return nil
	}
	ct := res.Header.Get("Content-Type")
	if !isTextual(ct) || isEventStream(ct) {
		return nil // binary / SSE / empty-CT — forward unscanned (never buffer a stream)
	}
	// Scan only a COMPLETE body: one with a bounded declared length, or one the
	// transport decompressed (res.Uncompressed — a gzip body is a complete unit,
	// not a stream; the transport decoded it and cleared Content-Length). A
	// genuinely chunked identity response (ContentLength < 0, not decompressed) is
	// a STREAM, so forward it unscanned rather than buffer it — buffering would
	// break incremental delivery for streaming textual responses. A declared
	// length over the cap is likewise forwarded unscanned.
	bounded := res.ContentLength >= 0 && res.ContentLength <= maxScanBytes
	if !bounded && !res.Uncompressed {
		return nil
	}
	if ce := res.Header.Get("Content-Encoding"); ce != "" && !strings.EqualFold(ce, "identity") {
		return fmt.Errorf("scrubResponse: unscannable Content-Encoding %q", ce) // fail closed
	}
	orig := res.Body
	// Read bounded (a decompressed body has ContentLength == -1, so don't trust it,
	// and a declared length can lie); +1 over the cap detects an oversized body.
	buf, err := io.ReadAll(io.LimitReader(orig, maxScanBytes+1))
	if err != nil {
		_ = orig.Close()
		res.Body = http.NoBody                                 // orig closed; avoid a (benign) double close
		return fmt.Errorf("scrubResponse: read body: %w", err) // fail CLOSED: a truncated body is unverified
	}
	if len(buf) == 0 {
		_ = orig.Close()
		res.Body = io.NopCloser(bytes.NewReader(nil)) // HEAD/204/304/empty — nothing to scan, don't touch Content-Length
		return nil
	}
	if int64(len(buf)) > maxScanBytes {
		// Oversized body (a decompressed body larger than the cap, or a lying
		// Content-Length): forward unscanned, stitching the bytes we already read
		// back ahead of the unread remainder.
		res.Body = bodyReadCloser{io.MultiReader(bytes.NewReader(buf), orig), orig}
		return nil
	}
	_ = orig.Close()
	scrubbed, _, rerr := g.keeper.RedactResponse(handle, buf)
	if rerr != nil {
		res.Body = http.NoBody // orig closed; avoid a (benign) double close
		return rerr            // fail closed
	}
	res.Body = io.NopCloser(bytes.NewReader(scrubbed))
	res.ContentLength = int64(len(scrubbed)) // == len(buf) when nothing was scrubbed
	res.Header.Set("Content-Length", strconv.Itoa(len(scrubbed)))
	res.Header.Del("Content-Encoding") // body is now plaintext identity
	return nil
}

// bodyReadCloser pairs a Reader (a MultiReader stitching already-read bytes ahead
// of the unread remainder of an oversized response) with the underlying body's
// Closer, so forwarding it unscanned still closes the upstream body.
type bodyReadCloser struct {
	io.Reader
	io.Closer
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
	case "application/json", "application/x-www-form-urlencoded", "application/xml":
		return true
	}
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	// RFC 6839 structured-syntax suffixes carry textual JSON/XML too — notably
	// application/problem+json (RFC 7807 verbose errors, a prime reflection surface)
	// and vnd.api+json / hal+json / *+xml.
	return strings.HasSuffix(ct, "+json") || strings.HasSuffix(ct, "+xml")
}

// isEventStream reports whether ct is a Server-Sent-Events stream
// (text/event-stream). isTextual returns true for it (it is text/*), but the
// response scrub must NOT buffer it — reading an SSE body to completion would
// break the LLM token streaming statusCapture.Flush exists to preserve — so the
// scrub gate excludes it explicitly and forwards it untouched.
func isEventStream(ct string) bool {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "text/event-stream")
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
