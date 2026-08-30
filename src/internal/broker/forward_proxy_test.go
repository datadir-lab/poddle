package broker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
func (interceptRO) Intercepts(string, string) bool { return true }

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

// TestForwardProxy_InterceptForcesHTTP1Upstream guards the HTTP/2 relay hang:
// re-origination must speak HTTP/1.1 so an HTTP/2-capable upstream's response is
// not relayed verbatim onto the HTTP/1.1 pod connection (a bogus "HTTP/2.0"
// status line with no HTTP/1 body framing hangs the pod's client).
func TestForwardProxy_InterceptForcesHTTP1Upstream(t *testing.T) {
	// The default re-origination transport pins ALPN to http/1.1.
	def, ok := NewForwardProxy(allowAll{}, nil).tr.(*http.Transport)
	if !ok || def.TLSClientConfig == nil {
		t.Fatalf("default upstream transport = %#v, want *http.Transport with a TLS config", def)
	}
	if np := def.TLSClientConfig.NextProtos; len(np) != 1 || np[0] != "http/1.1" {
		t.Fatalf("default upstream ALPN = %v, want [http/1.1]", np)
	}

	ca, err := tlsca.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// An HTTP/2-capable upstream; with ALPN forced to http/1.1 it serves 1.1.
	const body = "relayed over http/1.1"
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	}))
	up.EnableHTTP2 = true
	up.StartTLS()
	t.Cleanup(up.Close)

	fp := NewForwardProxy(interceptRO{}, &recAuditor{})
	fp.SetLeafSource(ca)
	upAddr := up.Listener.Addr().String()
	fp.tr = &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upAddr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}}, //nolint:gosec // test upstream, self-signed
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
	if resp.Proto != "HTTP/1.1" {
		t.Errorf("pod-facing proto = %q, want HTTP/1.1 (an HTTP/2 status line hangs the pod)", resp.Proto)
	}
	if resp.StatusCode != http.StatusOK || string(got) != body {
		t.Errorf("relayed = %d %q, want 200 %q", resp.StatusCode, got, body)
	}
}

// hostScopedRO intercepts only read.test and enforces read-only there; any other
// host is not intercepted (tunnelled).
type hostScopedRO struct{}

func (hostScopedRO) Check(_, _, method string) (bool, string) {
	if method == http.MethodGet || method == http.MethodConnect {
		return true, ""
	}
	return false, method + " not allowed (read-only)"
}
func (hostScopedRO) Intercepts(_, host string) bool { return host == "read.test" }

// TestForwardProxy_InterceptScopedByHost proves per-host routing: a matched host
// is TLS-terminated (POST visible → 403), an unmatched host is tunnelled (the
// proxy dials it directly, so a refused target yields a 502 CONNECT reply — never
// a "200 Connection established" + interception).
func TestForwardProxy_InterceptScopedByHost(t *testing.T) {
	ca, err := tlsca.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fp := NewForwardProxy(hostScopedRO{}, &recAuditor{})
	fp.SetLeafSource(ca)
	srv := httptest.NewServer(fp)
	t.Cleanup(srv.Close)

	// Matched host: intercepted → POST is blocked.
	tconn := dialIntercepted(t, srv.Listener.Addr().String(), ca, "tok")
	defer tconn.Close()
	br := bufio.NewReader(tconn)
	fmt.Fprintf(tconn, "POST /w HTTP/1.1\r\nHost: read.test\r\nContent-Length: 0\r\n\r\n")
	rp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read POST response: %v", err)
	}
	io.Copy(io.Discard, rp.Body)
	rp.Body.Close()
	if rp.StatusCode != http.StatusForbidden {
		t.Errorf("matched host POST = %d, want 403 (intercepted)", rp.StatusCode)
	}

	// Unmatched host: NOT intercepted → tunnelled. CONNECT to a refused port, so
	// the tunnel dial fails and the CONNECT reply is 502 (not a 200 established).
	raw, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	fmt.Fprintf(raw, "CONNECT 127.0.0.1:1 HTTP/1.1\r\nHost: 127.0.0.1:1\r\nProxy-Authorization: %s\r\n\r\n", basicToken("tok"))
	status, _ := bufio.NewReader(raw).ReadString('\n')
	if !strings.Contains(status, "502") {
		t.Errorf("unmatched host CONNECT = %q, want a 502 (tunnelled, not intercepted)", status)
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
// TestBlockedEgressIP pins the SSRF deny-floor: cloud-metadata + link-local are
// blocked; public, RFC1918 (legitimate internal), and loopback (datastores) are not.
func TestBlockedEgressIP(t *testing.T) {
	cases := []struct {
		ip    string
		block bool
	}{
		{"169.254.169.254", true}, // cloud IMDS
		{"169.254.0.1", true},     // link-local /16
		{"fe80::1", true},         // IPv6 link-local
		{"fd00:ec2::254", true},   // AWS IPv6 IMDS
		{"8.8.8.8", false},        // public
		{"10.0.0.5", false},       // RFC1918 — legitimate internal, not floored
		{"192.168.1.1", false},    // RFC1918
		{"127.0.0.1", false},      // loopback — legitimate (datastores), not floored
		{"::1", false},            // IPv6 loopback
	}
	for _, c := range cases {
		if got := blockedEgressIP(net.ParseIP(c.ip)); got != c.block {
			t.Errorf("blockedEgressIP(%s) = %v, want %v", c.ip, got, c.block)
		}
	}
	if blockedEgressIP(nil) {
		t.Error("blockedEgressIP(nil) = true, want false")
	}
}

// TestEgressDialControl covers the dial-guard hook and its escape hatch.
func TestEgressDialControl(t *testing.T) {
	ctrl := egressDialControl(false) // floor enabled
	if ctrl == nil {
		t.Fatal("egressDialControl(false) returned nil, want a guard func")
	}
	if err := ctrl("tcp", "169.254.169.254:80", nil); err == nil || !errors.Is(err, errBlockedEgress) {
		t.Errorf("Control(169.254.169.254:80) = %v, want errBlockedEgress", err)
	}
	if err := ctrl("tcp", "8.8.8.8:443", nil); err != nil {
		t.Errorf("Control(8.8.8.8:443) = %v, want nil (public host allowed)", err)
	}
	if egressDialControl(true) != nil { // PODDLE_ALLOW_LINK_LOCAL escape hatch
		t.Error("egressDialControl(true) should return nil (floor disabled)")
	}
}

// TestForwardProxy_BlocksMetadataEgress proves the floor holds end-to-end through
// the plain-HTTP forward path (the primary IMDS vector) REGARDLESS of policy: the
// allowAll policy would permit it, but the dial guard refuses it with a 403 and a
// "deny" audit record — and no connection to the metadata IP is ever made.
func TestForwardProxy_BlocksMetadataEgress(t *testing.T) {
	aud := &recAuditor{}
	srv := httptest.NewServer(NewForwardProxy(allowAll{}, aud))
	t.Cleanup(srv.Close)
	proxyURL, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	resp, err := client.Get("http://169.254.169.254/latest/meta-data/iam/security-credentials/")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (egress to IMDS blocked)", resp.StatusCode)
	}
	recs := aud.all()
	if len(recs) != 1 || recs[0].Decision != "deny" {
		t.Fatalf("audit = %+v, want one record with Decision=deny", recs)
	}
	if !strings.Contains(recs[0].Detail, "cloud-metadata") {
		t.Errorf("audit detail = %q, want it to mention cloud-metadata", recs[0].Detail)
	}
}

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

// egressChecker is a test policy: allows every method, intercepts, and reports a
// fixed egress mode. Satisfies PolicyChecker, InterceptChecker, and EgressModer.
type egressChecker struct{ mode string }

func (egressChecker) Check(string, string, string) (bool, string) { return true, "" }
func (egressChecker) Intercepts(string, string) bool              { return true }
func (e egressChecker) EgressMode(string) string                  { return e.mode }

func TestForwardProxy_egressMode(t *testing.T) {
	// Defaults to "redact" when the policy carries no EgressModer.
	if got := NewForwardProxy(allowAll{}, nil).egressMode("t"); got != "redact" {
		t.Errorf("egressMode with no EgressModer = %q, want redact", got)
	}
	// An empty mode from the policy also defaults to redact.
	if got := NewForwardProxy(egressChecker{mode: ""}, nil).egressMode("t"); got != "redact" {
		t.Errorf("egressMode empty = %q, want redact", got)
	}
	// Otherwise the policy's mode wins.
	if got := NewForwardProxy(egressChecker{mode: "block"}, nil).egressMode("t"); got != "block" {
		t.Errorf("egressMode = %q, want block", got)
	}
}

// TestForwardProxy_InterceptRedactsBody proves egress DLP on the decrypted HTTPS
// request body: per the pod's egress mode, a secret in an intercepted POST is
// scrubbed before it reaches the upstream (redact), rejected (block), or left as
// is (off). An echo upstream records exactly what egress received.
func TestForwardProxy_InterceptRedactsBody(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE" // AWS access-key id shape
	body := "token=" + secret

	run := func(t *testing.T, mode string) (podCode int, upstreamGot []byte, aud *recAuditor) {
		ca, err := tlsca.Load(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		var got []byte
		up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(up.Close)

		aud = &recAuditor{}
		fp := NewForwardProxy(egressChecker{mode: mode}, aud)
		fp.SetLeafSource(ca)
		upAddr := up.Listener.Addr().String()
		fp.tr = &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, upAddr)
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}}, //nolint:gosec // test upstream
		}
		srv := httptest.NewServer(fp)
		t.Cleanup(srv.Close)

		tconn := dialIntercepted(t, srv.Listener.Addr().String(), ca, "tok")
		defer tconn.Close()
		br := bufio.NewReader(tconn)
		fmt.Fprintf(tconn, "POST /p HTTP/1.1\r\nHost: read.test\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode, got, aud
	}

	t.Run("redact scrubs before egress", func(t *testing.T) {
		code, got, aud := run(t, "redact")
		if code != http.StatusOK {
			t.Errorf("pod status = %d, want 200", code)
		}
		if bytes.Contains(got, []byte(secret)) {
			t.Errorf("secret leaked to upstream: %q", got)
		}
		if !bytes.Contains(got, []byte(RedactPlaceholder)) {
			t.Errorf("upstream did not receive placeholder: %q", got)
		}
		if !auditHas(aud, "POST", "redact") {
			t.Errorf("no redact audit record: %+v", aud.all())
		}
	})

	t.Run("block rejects and sends nothing upstream", func(t *testing.T) {
		code, got, aud := run(t, "block")
		if code != http.StatusForbidden {
			t.Errorf("pod status = %d, want 403", code)
		}
		if len(got) != 0 {
			t.Errorf("upstream received a body despite block: %q", got)
		}
		if !auditHas(aud, "POST", "block") {
			t.Errorf("no block audit record: %+v", aud.all())
		}
	})

	t.Run("off forwards the body untouched", func(t *testing.T) {
		code, got, _ := run(t, "off")
		if code != http.StatusOK {
			t.Errorf("pod status = %d, want 200", code)
		}
		if !bytes.Contains(got, []byte(secret)) {
			t.Errorf("off mode altered the body: %q", got)
		}
	})
}

// auditHas reports whether the auditor recorded a request with the given method
// and decision (waiting briefly, since intercept emits on its own goroutine).
func auditHas(a *recAuditor, method, decision string) bool {
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		for _, rec := range a.all() {
			if rec.Method == method && rec.Decision == decision {
				return true
			}
		}
	}
	return false
}

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
