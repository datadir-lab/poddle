package broker

import (
	"encoding/base64"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// Gateway is the secretless egress proxy. A pod's harness points at it
// (BASE_URL) and presents a handle (in Authorization); the gateway resolves the
// handle to a Credential, injects the REAL secret per the credential's mode, and
// reverse-proxies to the vendor. The real secret never leaves the broker.
type Gateway struct {
	handles *Handles
}

// NewGateway returns a gateway backed by the handle registry.
func NewGateway(h *Handles) *Gateway { return &Gateway{handles: h} }

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
