package broker

import (
	"bufio"
	"crypto/tls"
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
// blocking denied destinations.
//
// By default a CONNECT is tunnelled opaquely (no TLS inspection). When the pod's
// policy opts into interception AND a LeafSource is wired, the proxy instead
// terminates TLS (presenting a leaf the pod trusts) so per-request method rules
// apply on HTTPS; the pod must trust the egress CA (injected at `up`).
type ForwardProxy struct {
	policy  PolicyChecker     // reused: Check(token, host, method); daemon maps token -> pod -> policy
	auditor Auditor           // reused: one record per egress attempt
	leaves  LeafSource        // nil = interception unavailable (always tunnel opaquely)
	tr      http.RoundTripper // re-originates intercepted requests; system roots, no proxy-env
}

// NewForwardProxy returns a forward proxy governed by pc and audited by a.
func NewForwardProxy(pc PolicyChecker, a Auditor) *ForwardProxy {
	// The re-origination transport uses the system roots and ignores HTTP(S)_PROXY
	// (so an intercept verifies the real upstream and never loops back through this
	// proxy), and it speaks HTTP/1.1 ONLY. The pod-facing leg is HTTP/1.1, so an
	// HTTP/2 upstream response must not be relayed verbatim: its "HTTP/2.0" status
	// line is invalid over HTTP/1.1 and it carries no HTTP/1 body framing, which
	// hangs the pod's client. Pinning ALPN to http/1.1 keeps the relay 1.1 end to
	// end. RootCAs stays nil (system roots) so upstream verification is unchanged.
	return &ForwardProxy{policy: pc, auditor: a, tr: &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}},
	}}
}

// SetLeafSource enables TLS interception for pods whose policy opts in, using ls
// to mint per-host leaf certificates. Without it, every CONNECT is tunnelled.
func (f *ForwardProxy) SetLeafSource(ls LeafSource) { f.leaves = ls }

// intercepts reports whether this CONNECT should be TLS-terminated: the pod opted
// in and a leaf source is available.
func (f *ForwardProxy) intercepts(token string) bool {
	if f.leaves == nil {
		return false
	}
	ic, ok := f.policy.(InterceptChecker)
	return ok && ic.Intercepts(token)
}

// monitored reports whether the pod's policy is in monitor mode.
func (f *ForwardProxy) monitored(token string) bool {
	mc, ok := f.policy.(MonitorChecker)
	return ok && mc.Monitored(token)
}

// egressMode returns the pod's egress redaction mode. It defaults to "redact"
// (matching NewRedactor("") and the system default) when the policy does not
// implement EgressModer or reports no mode.
func (f *ForwardProxy) egressMode(token string) string {
	if em, ok := f.policy.(EgressModer); ok {
		if m := em.EgressMode(token); m != "" {
			return m
		}
	}
	return "redact"
}

func (f *ForwardProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := proxyAuthToken(r.Header.Get("Proxy-Authorization"))
	host := destinationHost(r)

	// Destination-level check (CONNECT hides the method, so it is skipped there).
	// In monitor mode a would-be denial is forwarded (not blocked) and recorded as
	// "monitor" instead of "deny", so a policy can be rolled out before enforcing.
	var monitored string
	if f.policy != nil {
		if allow, reason := f.policy.Check(token, host, r.Method); !allow {
			if f.monitored(token) {
				monitored = reason
			} else {
				http.Error(w, "poddle: egress blocked by policy: "+reason, http.StatusForbidden)
				f.emit(token, host, r.Method, "deny", reason, http.StatusForbidden)
				return
			}
		}
	}

	if r.Method == http.MethodConnect {
		if f.intercepts(token) {
			f.intercept(w, r, token, host, monitored)
		} else {
			f.tunnel(w, r, token, host, monitored)
		}
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

// intercept terminates TLS on an opted-in pod's CONNECT so per-request method
// rules apply on HTTPS. It presents a leaf the pod trusts (minted for the SNI),
// reads each request, enforces the policy against the now-visible method, and
// re-originates to the real upstream over TLS. hostMonitored is a would-be
// destination denial being observed (monitor mode) rather than blocked.
func (f *ForwardProxy) intercept(w http.ResponseWriter, r *http.Request, token, host, hostMonitored string) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}

	tconn := tls.Server(client, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"}, // parse the decrypted stream as HTTP/1.1
		GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := chi.ServerName
			if name == "" {
				name = host
			}
			return f.leaves.LeafFor(name)
		},
	})
	if err := tconn.Handshake(); err != nil {
		return // pod rejected the leaf (e.g. certificate pinning) — cannot inspect
	}
	defer func() { _ = tconn.Close() }()

	br := bufio.NewReader(tconn)
	upstream := &http.Client{Transport: f.tr} // real roots verify the upstream
	monitorMode := f.monitored(token)

	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return // pod closed the tunnel
		}
		// Per-request policy: the method is now visible on HTTPS.
		decision, detail := "allow", ""
		if hostMonitored != "" {
			decision, detail = "monitor", "would deny: "+hostMonitored
		} else if f.policy != nil {
			if allow, reason := f.policy.Check(token, host, req.Method); !allow {
				if monitorMode {
					decision, detail = "monitor", "would deny: "+reason
				} else {
					drain(req.Body)
					writeStatus(tconn, req, http.StatusForbidden, "poddle: blocked by policy: "+reason)
					f.emit(token, host, req.Method, "deny", reason, http.StatusForbidden)
					continue
				}
			}
		}

		out, err := http.NewRequest(req.Method, "https://"+host+req.URL.RequestURI(), req.Body)
		if err != nil {
			drain(req.Body)
			writeStatus(tconn, req, http.StatusBadRequest, "bad request")
			continue
		}
		out.Header = req.Header.Clone()
		out.Header.Del("Connection")
		out.Header.Del("Proxy-Connection")
		out.ContentLength = req.ContentLength
		out.Host = host
		resp, err := upstream.Do(out)
		if err != nil {
			writeStatus(tconn, req, http.StatusBadGateway, "bad gateway")
			f.emit(token, host, req.Method, decision, "upstream error", http.StatusBadGateway)
			continue
		}
		status := resp.StatusCode
		werr := resp.Write(tconn)
		_ = resp.Body.Close()
		f.emit(token, host, req.Method, decision, detail, status)
		if werr != nil {
			return // pod hung up mid-response
		}
	}
}

// writeStatus sends a minimal framed HTTP/1.1 response over the intercepted
// connection (a policy block or proxy error), keeping the tunnel alive.
func writeStatus(w io.Writer, req *http.Request, code int, body string) {
	resp := &http.Response{
		StatusCode: code, ProtoMajor: 1, ProtoMinor: 1, Request: req,
		Header:        http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
	_ = resp.Write(w)
}

// drain consumes and discards a request body so the keep-alive stream is
// positioned for the next request even when the body was not forwarded.
func drain(b io.ReadCloser) {
	if b != nil {
		_, _ = io.Copy(io.Discard, b)
		_ = b.Close()
	}
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
