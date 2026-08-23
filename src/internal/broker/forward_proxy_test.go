package broker

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datadir-lab/poddle/src/internal/tlsca"
)

// interceptRO is a read-only-web checker: GET/CONNECT allowed, other methods
// denied, and the pod opts into interception.
type interceptRO struct{}

func (interceptRO) Check(handle, host, method string) (bool, string) {
	if method == http.MethodGet || method == http.MethodConnect {
		return true, ""
	}
	return false, method + " not allowed (read-only)"
}
func (interceptRO) Intercepts(string) bool { return true }

// TestForwardProxy_InterceptEnforcesMethodOnHTTPS proves the headline capability:
// with interception on, the proxy terminates the pod's HTTPS CONNECT, sees the
// real method, and blocks a POST (that a plain tunnel could never inspect) while
// letting a GET through.
func TestForwardProxy_InterceptEnforcesMethodOnHTTPS(t *testing.T) {
	ca, err := tlsca.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	aud := &recAuditor{}
	fp := NewForwardProxy(interceptRO{}, aud)
	fp.SetLeafSource(ca)
	srv := httptest.NewServer(fp)
	t.Cleanup(srv.Close)

	// Raw CONNECT to the proxy, then TLS-handshake THROUGH it, trusting the CA
	// (as an intercepted pod would).
	tconn := dialIntercepted(t, srv.Listener.Addr().String(), ca, "poddle_egr_x")
	defer tconn.Close()
	br := bufio.NewReader(tconn)

	// POST is blocked by the proxy (403) — visible only because TLS was terminated.
	fmt.Fprintf(tconn, "POST /write HTTP/1.1\r\nHost: read.test\r\nContent-Length: 0\r\n\r\n")
	rp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read POST response: %v", err)
	}
	io.Copy(io.Discard, rp.Body)
	rp.Body.Close()
	if rp.StatusCode != http.StatusForbidden {
		t.Errorf("POST over HTTPS should be blocked (403), got %d", rp.StatusCode)
	}

	// GET is permitted — forwarded to the (unresolvable) upstream, so it fails at
	// the network, not the policy: a 502 with an "allow" decision.
	fmt.Fprintf(tconn, "GET /page HTTP/1.1\r\nHost: read.test\r\n\r\n")
	rg, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read GET response: %v", err)
	}
	io.Copy(io.Discard, rg.Body)
	rg.Body.Close()

	// The audit shows a method-denied POST and a permitted GET. The proxy emits
	// each record on its own goroutine after writing the response, so wait for
	// both decisions to land rather than racing the emit (which -race exposes).
	var sawDeny, sawAllow bool
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		sawDeny, sawAllow = false, false
		for _, rec := range aud.all() {
			switch {
			case rec.Method == http.MethodPost && rec.Decision == "deny":
				sawDeny = true
			case rec.Method == http.MethodGet && rec.Decision == "allow":
				sawAllow = true
			}
		}
		if sawDeny && sawAllow {
			break
		}
	}
	if !sawDeny {
		t.Errorf("expected a deny for the POST; audit = %+v", aud.all())
	}
	if !sawAllow {
		t.Errorf("expected an allow for the GET; audit = %+v", aud.all())
	}
}

// TestForwardProxy_InterceptRelaysUpstreamResponse proves the success path: an
// allowed GET is decrypted, re-originated to a real HTTPS upstream, and that
// upstream's status, body, and headers are relayed back down the intercepted
// tunnel to the pod (and audited as an allow with the upstream's status).
func TestForwardProxy_InterceptRelaysUpstreamResponse(t *testing.T) {
	ca, err := tlsca.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const body = "hello from the real upstream"
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/page" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.Header().Set("X-Upstream", "yes")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(up.Close)

	aud := &recAuditor{}
	fp := NewForwardProxy(interceptRO{}, aud)
	fp.SetLeafSource(ca)
	// Re-originate every intercepted request to the test upstream, trusting its
	// self-signed cert. The pod-facing leg still uses the real CA-minted leaf.
	upAddr := up.Listener.Addr().String()
	fp.tr = &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upAddr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test upstream, self-signed
	}
	srv := httptest.NewServer(fp)
	t.Cleanup(srv.Close)

	tconn := dialIntercepted(t, srv.Listener.Addr().String(), ca, "poddle_egr_x")
	defer tconn.Close()
	br := bufio.NewReader(tconn)

	fmt.Fprintf(tconn, "GET /page HTTP/1.1\r\nHost: read.test\r\n\r\n")
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read GET response: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("relayed status = %d, want 200", resp.StatusCode)
	}
	if string(got) != body {
		t.Errorf("relayed body = %q, want %q", got, body)
	}
	if resp.Header.Get("X-Upstream") != "yes" {
		t.Errorf("upstream header not relayed: %+v", resp.Header)
	}

	var sawAllow bool
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		sawAllow = false
		for _, rec := range aud.all() {
			if rec.Method == http.MethodGet && rec.Decision == "allow" && rec.Status == http.StatusOK {
				sawAllow = true
			}
		}
		if sawAllow {
			break
		}
	}
	if !sawAllow {
		t.Errorf("expected an allow@200 for the GET; audit = %+v", aud.all())
	}
}

// dialIntercepted opens a CONNECT to the proxy and completes the TLS handshake
// through it, trusting the egress CA as an intercepted pod would, returning the
// live TLS connection to the (terminated) tunnel.
func dialIntercepted(t *testing.T, proxyAddr string, ca *tlsca.Authority, token string) *tls.Conn {
	t.Helper()
	raw, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(raw, "CONNECT read.test:443 HTTP/1.1\r\nHost: read.test:443\r\nProxy-Authorization: %s\r\n\r\n", basicToken(token))
	hb := bufio.NewReader(raw)
	if status, _ := hb.ReadString('\n'); !strings.Contains(status, "200") {
		_ = raw.Close()
		t.Fatalf("CONNECT reply = %q, want 200", status)
	}
	for { // consume headers to the blank line
		line, _ := hb.ReadString('\n')
		if line == "\r\n" || line == "\n" || line == "" {
			break
		}
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert())
	tconn := tls.Client(raw, &tls.Config{RootCAs: pool, ServerName: "read.test"})
	if err := tconn.Handshake(); err != nil {
		_ = raw.Close()
		t.Fatalf("intercepted handshake failed: %v", err)
	}
	return tconn
}

// recAuditor records every egress decision the proxy emits. Proxy is called from
// the proxy's handler goroutine while the test reads the records, so access is
// mutex-guarded (the -race build flags an unsynchronised slice otherwise).
type recAuditor struct {
	mu      sync.Mutex
	records []ProxyRecord
}

func (a *recAuditor) Proxy(r ProxyRecord) {
	a.mu.Lock()
	a.records = append(a.records, r)
	a.mu.Unlock()
}

func (a *recAuditor) all() []ProxyRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ProxyRecord(nil), a.records...)
}

type allowAll struct{}

func (allowAll) Check(string, string, string) (bool, string) { return true, "" }

func basicToken(token string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(token+":x"))
}

// connect opens a raw CONNECT to the proxy and returns the status line plus the
// live tunnel reader/conn (when established).
func connect(t *testing.T, proxyAddr, target, token string) (string, *bufio.Reader, net.Conn) {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n",
		target, target, basicToken(token))
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("reading CONNECT status: %v", err)
	}
	return status, br, conn
}

// TestForwardProxy_TunnelsAllowedConnect covers the HTTPS/CONNECT egress path:
// an allowed CONNECT establishes a tunnel, bytes splice end-to-end, and the
// attempt is audited as allowed.
func TestForwardProxy_TunnelsAllowedConnect(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	go func() {
		c, err := target.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.Copy(c, c) // echo server
	}()
	targetAddr := target.Addr().String()
	targetHost, _, _ := net.SplitHostPort(targetAddr)

	aud := &recAuditor{}
	fp := httptest.NewServer(NewForwardProxy(hostAllow{host: targetHost}, aud))
	t.Cleanup(fp.Close)

	status, br, conn := connect(t, fp.Listener.Addr().String(), targetAddr, "poddle_egr_a")
	defer conn.Close()
	if !strings.Contains(status, "200") {
		t.Fatalf("CONNECT reply = %q, want 200 Connection established", status)
	}
	if _, err := br.ReadString('\n'); err != nil { // consume the blank line
		t.Fatal(err)
	}

	// The tunnel is raw now: what we write comes back from the echo target.
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatalf("reading tunnelled echo: %v", err)
	}
	if string(buf) != "ping" {
		t.Errorf("tunnel echo = %q, want ping", buf)
	}
	if recs := aud.all(); len(recs) == 0 || recs[0].Decision != "allow" {
		t.Errorf("expected an allow audit record for the tunnel, got %+v", recs)
	}
}

// TestForwardProxy_BlocksDeniedConnect: a CONNECT to a non-allow-listed host is
// refused with 403 and audited as a deny — the tunnel is never opened.
func TestForwardProxy_BlocksDeniedConnect(t *testing.T) {
	aud := &recAuditor{}
	fp := httptest.NewServer(NewForwardProxy(hostAllow{host: "allowed.only.invalid"}, aud))
	t.Cleanup(fp.Close)

	status, _, conn := connect(t, fp.Listener.Addr().String(), "blocked.example.invalid:443", "poddle_egr_b")
	defer conn.Close()
	if !strings.Contains(status, "403") {
		t.Fatalf("denied CONNECT status = %q, want 403", status)
	}
	if recs := aud.all(); len(recs) == 0 || recs[0].Decision != "deny" {
		t.Errorf("expected a deny audit record, got %+v", recs)
	}
}

// TestForwardProxy_MonitorMode_TunnelsWouldDeny: under monitor mode a would-be
// denied CONNECT is tunnelled (not refused) and audited as "monitor".
func TestForwardProxy_MonitorMode_TunnelsWouldDeny(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	go func() {
		c, err := target.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.Copy(c, c)
	}()

	aud := &recAuditor{}
	fp := httptest.NewServer(NewForwardProxy(monitorChecker{}, aud)) // denies via Check, but Monitored
	t.Cleanup(fp.Close)

	status, br, conn := connect(t, fp.Listener.Addr().String(), target.Addr().String(), "poddle_egr_m")
	defer conn.Close()
	if !strings.Contains(status, "200") {
		t.Fatalf("monitor mode should establish the tunnel; status = %q", status)
	}
	if _, err := br.ReadString('\n'); err != nil { // consume the blank line
		t.Fatal(err)
	}
	// Round-trip through the tunnel: the copy goroutines (and thus the emit that
	// precedes them) have run by the time the echo returns.
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatalf("reading tunnelled echo: %v", err)
	}
	if recs := aud.all(); len(recs) == 0 || recs[0].Decision != "monitor" {
		t.Errorf("expected a monitor audit record, got %+v", recs)
	}
}

// TestForwardProxy_ConnectUpstreamUnreachable: an allowed CONNECT to an
// unreachable target returns 502 rather than hanging.
func TestForwardProxy_ConnectUpstreamUnreachable(t *testing.T) {
	aud := &recAuditor{}
	fp := httptest.NewServer(NewForwardProxy(hostAllow{host: "127.0.0.1"}, aud))
	t.Cleanup(fp.Close)

	status, _, conn := connect(t, fp.Listener.Addr().String(), "127.0.0.1:1", "poddle_egr_c")
	defer conn.Close()
	if !strings.Contains(status, "502") {
		t.Errorf("CONNECT to an unreachable target = %q, want 502", status)
	}
}

// TestForwardProxy_ForwardUpstreamError: an allowed plain-HTTP destination that
// is unreachable returns 502 (and is still audited).
func TestForwardProxy_ForwardUpstreamError(t *testing.T) {
	aud := &recAuditor{}
	fp := httptest.NewServer(NewForwardProxy(allowAll{}, aud))
	t.Cleanup(fp.Close)

	resp, err := proxyClient(t, fp.URL).Get("http://127.0.0.1:1/x") // port 1: refused
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("unreachable upstream = %d, want 502", resp.StatusCode)
	}
}

func TestProxyAuthToken(t *testing.T) {
	if got := proxyAuthToken(basicToken("mytoken")); got != "mytoken" {
		t.Errorf("valid Basic: got %q, want mytoken", got)
	}
	if got := proxyAuthToken("Bearer abc"); got != "" {
		t.Errorf("non-Basic scheme should yield no token, got %q", got)
	}
	if got := proxyAuthToken("Basic !!!not-base64!!!"); got != "" {
		t.Errorf("undecodable base64 should yield no token, got %q", got)
	}
	if got := proxyAuthToken(""); got != "" {
		t.Errorf("empty header should yield no token, got %q", got)
	}
}

func TestDestinationHost(t *testing.T) {
	httpReq := httptest.NewRequest(http.MethodGet, "http://example.com:8080/path", nil)
	if got := destinationHost(httpReq); got != "example.com" {
		t.Errorf("HTTP host = %q, want example.com (port stripped)", got)
	}
	connReq := httptest.NewRequest(http.MethodConnect, "example.com:443", nil)
	if got := destinationHost(connReq); got != "example.com" {
		t.Errorf("CONNECT host = %q, want example.com", got)
	}
}
