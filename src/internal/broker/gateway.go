package broker

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// maxScanBytes bounds egress redaction; larger bodies are forwarded unscanned.
const maxScanBytes = 25 << 20

// Gateway is the secretless egress proxy. A pod's harness points at it
// (BASE_URL) and presents a handle (in Authorization); the gateway resolves the
// handle to a Credential, injects the REAL secret per the credential's mode, and
// reverse-proxies to the vendor. The real secret never leaves the broker. It
// also redacts secrets from outbound bodies (egress DLP).
type Gateway struct {
	handles  *Handles
	redactor *Redactor
}

// NewGateway returns a gateway backed by the handle registry, redacting egress
// by default (mode "redact").
func NewGateway(h *Handles) *Gateway { return &Gateway{handles: h, redactor: NewRedactor("redact")} }

// SetEgressMode configures egress redaction: "redact" (default), "block", "off".
func (g *Gateway) SetEgressMode(mode string) { g.redactor = NewRedactor(mode) }

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cred, err := g.handles.Resolve(handleFromAuth(r.Header.Get("Authorization")))
	if err != nil {
		// Challenge so git (which doesn't send Basic preemptively) retries with
		// the handle it has in the URL creds.
		w.Header().Set("WWW-Authenticate", `Basic realm="poddle"`)
		http.Error(w, "invalid or revoked handle", http.StatusUnauthorized)
		return
	}
	up, err := url.Parse(cred.BaseURL)
	if err != nil {
		http.Error(w, "bad upstream", http.StatusBadGateway)
		return
	}

	// Egress redaction: scrub secrets from textual bodies (LLM/API JSON). Git
	// and other binary payloads are skipped so packfiles aren't buffered/mangled.
	if !g.redactBody(w, r, cred) {
		return // blocked
	}

	proxy := httputil.NewSingleHostReverseProxy(up)
	base := proxy.Director
	proxy.Director = func(req *http.Request) {
		base(req)          // scheme/host/path -> upstream
		req.Host = up.Host // match the upstream Host header
		applyAuth(req.Header, cred)
	}
	proxy.ServeHTTP(w, r)
}

// applyAuth clears the incoming handle and injects the real secret in the header
// the credential's mode expects.
func applyAuth(h http.Header, cred Credential) {
	h.Del("Authorization")
	h.Del("X-Api-Key")
	switch cred.Mode {
	case ModeAPIKey:
		h.Set("X-Api-Key", cred.Secret)
	case ModeSubscription:
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
// It returns false (after writing a 403) when redaction is set to block and a
// secret is found; true otherwise, including when nothing is scanned.
func (g *Gateway) redactBody(w http.ResponseWriter, r *http.Request, cred Credential) bool {
	if g.redactor == nil || r.Body == nil || !isTextual(r.Header.Get("Content-Type")) || r.ContentLength > maxScanBytes {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxScanBytes))
	_ = r.Body.Close()
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		return true
	}
	managed := []string{cred.Secret}
	if _, tok, ok := strings.Cut(cred.Secret, ":"); ok {
		managed = append(managed, tok) // basic: also scrub the token half of user:token
	}
	red, _, block := g.redactor.Scan(body, managed...)
	if block {
		http.Error(w, "poddle: outbound request blocked — secret detected", http.StatusForbidden)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(red))
	r.ContentLength = int64(len(red))
	r.Header.Set("Content-Length", strconv.Itoa(len(red)))
	return true
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
