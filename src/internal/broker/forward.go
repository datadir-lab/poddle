package broker

import (
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
)

// ForwardProxy is the broker's egress forward proxy for a pod's ARBITRARY
// (non-brokered) traffic. The pod is wired HTTP_PROXY/HTTPS_PROXY at it and
// authenticates with a per-pod egress token in Proxy-Authorization. The proxy
// identifies the pod by that token, checks the pod's governance policy against
// the destination host (and method, for plain HTTP), audits the decision, and
// forwards allowed traffic (plain HTTP) or tunnels it (CONNECT / HTTPS) —
// blocking denied destinations. It never inspects TLS payloads (no MITM).
type ForwardProxy struct {
	policy  PolicyChecker // reused: Check(token, host, method); daemon maps token -> pod -> policy
	auditor Auditor       // reused: one record per egress attempt
}

// NewForwardProxy returns a forward proxy governed by pc and audited by a.
func NewForwardProxy(pc PolicyChecker, a Auditor) *ForwardProxy {
	return &ForwardProxy{policy: pc, auditor: a}
}

func (f *ForwardProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := proxyAuthToken(r.Header.Get("Proxy-Authorization"))
	host := destinationHost(r)

	// In monitor mode a would-be denial is forwarded (not blocked) and recorded as
	// "monitor" instead of "deny", so a policy can be rolled out before enforcing.
	var monitored string
	if f.policy != nil {
		if allow, reason := f.policy.Check(token, host, r.Method); !allow {
			if mc, ok := f.policy.(MonitorChecker); ok && mc.Monitored(token) {
				monitored = reason
			} else {
				http.Error(w, "poddle: egress blocked by policy: "+reason, http.StatusForbidden)
				f.emit(token, host, r.Method, "deny", reason, http.StatusForbidden)
				return
			}
		}
	}

	if r.Method == http.MethodConnect {
		f.tunnel(w, r, token, host, monitored)
		return
	}
	f.forward(w, r, token, host, monitored)
}

// decOrMonitor overrides an allow decision with "monitor" when the request would
// have been denied under enforcement (monitor mode).
func decOrMonitor(monitored, decision, detail string) (string, string) {
	if monitored != "" {
		return "monitor", "would deny: " + monitored
	}
	return decision, detail
}

// tunnel handles CONNECT: dial the target and splice the two connections, so TLS
// stays end-to-end (the proxy never sees the plaintext).
func (f *ForwardProxy) tunnel(w http.ResponseWriter, r *http.Request, token, host, monitored string) {
	dst, err := net.Dial("tcp", r.Host)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		f.emit(token, host, r.Method, "allow", "upstream unreachable", http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		_ = dst.Close()
		return
	}
	src, _, err := hj.Hijack()
	if err != nil {
		_ = dst.Close()
		return
	}
	_, _ = src.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	dec, det := decOrMonitor(monitored, "allow", "tunnel")
	f.emit(token, host, r.Method, dec, det, http.StatusOK)
	go func() { _, _ = io.Copy(dst, src); _ = dst.Close() }()
	go func() { _, _ = io.Copy(src, dst); _ = src.Close() }()
}

// forward proxies a plain-HTTP request to its destination.
func (f *ForwardProxy) forward(w http.ResponseWriter, r *http.Request, token, host, monitored string) {
	out, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	for k, vs := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "proxy-") {
			continue // don't forward proxy hop headers
		}
		for _, v := range vs {
			out.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultTransport.RoundTrip(out)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		f.emit(token, host, r.Method, "allow", "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	dec, det := decOrMonitor(monitored, "allow", "")
	f.emit(token, host, r.Method, dec, det, resp.StatusCode)
}

func (f *ForwardProxy) emit(token, host, method, decision, detail string, status int) {
	if f.auditor == nil {
		return
	}
	f.auditor.Proxy(ProxyRecord{
		Handle: token, Upstream: host, Method: method,
		Decision: decision, Detail: detail, Status: status,
	})
}

// destinationHost returns the target hostname for a proxied request: r.Host for
// CONNECT (host:port), else the request URL's host.
func destinationHost(r *http.Request) string {
	h := r.Host
	if r.Method != http.MethodConnect && r.URL.Host != "" {
		h = r.URL.Host
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

// proxyAuthToken extracts the egress token from a Basic Proxy-Authorization
// header (username half).
func proxyAuthToken(h string) string {
	v, ok := strings.CutPrefix(h, "Basic ")
	if !ok {
		return ""
	}
	dec, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return ""
	}
	user, _, _ := strings.Cut(string(dec), ":")
	return user
}
