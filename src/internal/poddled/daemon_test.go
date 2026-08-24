package poddled

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datadir-lab/poddle/src/internal/audit"
	"github.com/datadir-lab/poddle/src/internal/broker"
)

// fakeBroker records what the daemon asks of the broker.
type fakeBroker struct {
	stored  []broker.Credential
	issued  int
	revoked []string
	egress  string
	addr    string
}

func (f *fakeBroker) Store(c broker.Credential) (string, error) {
	f.stored = append(f.stored, c)
	return fmt.Sprintf("cred%d", len(f.stored)), nil
}
func (f *fakeBroker) IssueHandle(credID, scope string, _ time.Duration) (broker.Handle, error) {
	f.issued++
	return broker.Handle{Value: "poddle_" + credID, Scope: scope}, nil
}
func (f *fakeBroker) Revoke(v string) { f.revoked = append(f.revoked, v) }
func (f *fakeBroker) Resolve(string) (broker.Credential, error) {
	return broker.Credential{BaseURL: "redis://:realpass@127.0.0.1:6379"}, nil
}
func (f *fakeBroker) Serve(string) (string, error) {
	f.addr = "0.0.0.0:9999"
	return f.addr, nil
}
func (f *fakeBroker) Addr() string               { return f.addr }
func (f *fakeBroker) SetEgressMode(m string)     { f.egress = m }
func (f *fakeBroker) Stop(context.Context) error { return nil }

func TestDaemon_AuditsHandleIssueAndPostedEvents(t *testing.T) {
	store, err := audit.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	d := New(&fakeBroker{}, store)
	if _, err := d.Start("0.0.0.0:0", "redact", "", "", ""); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)

	// Issuing a handle records a handle.issue event (never the secret).
	issue := `{"scope":"proj/svc","credential":{"mode":"subscription","vendor":"anthropic","secret":"SENTINEL","baseURL":"http://up"}}`
	if r, err := http.Post(srv.URL+"/pods/proj/handles", "application/json", strings.NewReader(issue)); err != nil {
		t.Fatal(err)
	} else {
		_ = r.Body.Close()
	}
	// A client-posted lifecycle event lands too.
	ev, _ := json.Marshal(audit.Event{Pod: "proj", Kind: audit.KindPodUp, Detail: "size=weak"})
	if r, err := http.Post(srv.URL+"/audit", "application/json", bytes.NewReader(ev)); err != nil {
		t.Fatal(err)
	} else {
		_ = r.Body.Close()
	}

	resp, err := http.Get(srv.URL + "/audit?pod=proj")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var events []audit.Event
	if err := json.Unmarshal(body, &events); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	kinds := map[audit.Kind]bool{}
	for _, e := range events {
		kinds[e.Kind] = true
	}
	if !kinds[audit.KindHandleIssue] {
		t.Errorf("expected a handle.issue event; got %+v", events)
	}
	if !kinds[audit.KindPodUp] {
		t.Errorf("expected the posted pod.up event; got %+v", events)
	}
	if strings.Contains(string(body), "SENTINEL") {
		t.Error("the audit log must never contain the secret")
	}

	// The tamper-evident chain verifies through the control API.
	vr, err := http.Get(srv.URL + "/audit/verify")
	if err != nil {
		t.Fatal(err)
	}
	defer vr.Body.Close()
	var v struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(vr.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if !v.OK {
		t.Error("audit chain should verify intact")
	}
}

func testServer(t *testing.T) (*httptest.Server, *fakeBroker) {
	t.Helper()
	fb := &fakeBroker{}
	d := New(fb, nil)
	if _, err := d.Start("0.0.0.0:0", "redact", "", "", ""); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)
	return srv, fb
}

func TestDaemon_Health(t *testing.T) {
	srv, _ := testServer(t)
	resp, err := http.Get(srv.URL + "/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("health: %v status=%v", err, resp.StatusCode)
	}
}

func TestDaemon_Gateway(t *testing.T) {
	srv, fb := testServer(t)
	if fb.egress != "redact" {
		t.Errorf("Start should set egress before serve, got %q", fb.egress)
	}
	resp, _ := http.Get(srv.URL + "/gateway")
	var got map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got["addr"] != "0.0.0.0:9999" {
		t.Errorf("gateway addr = %q", got["addr"])
	}
}

func issue(t *testing.T, url, pod string) string {
	t.Helper()
	body, _ := json.Marshal(issueReq{Scope: pod, Credential: broker.Credential{Mode: broker.ModeSubscription, Secret: "s", BaseURL: "http://x"}})
	resp, err := http.Post(url+"/pods/"+pod+"/handles", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("issue: %v status=%v", err, resp.StatusCode)
	}
	var got map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&got)
	return got["handle"]
}

func TestDaemon_IssueHandle(t *testing.T) {
	srv, fb := testServer(t)
	h := issue(t, srv.URL, "box")
	if h == "" {
		t.Fatal("no handle returned")
	}
	if len(fb.stored) != 1 || fb.issued != 1 {
		t.Errorf("store/issue = %d/%d, want 1/1", len(fb.stored), fb.issued)
	}
}

// resp builds a RESP array-of-bulk-strings command.
func resp(parts ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(parts))
	for _, p := range parts {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(p), p)
	}
	return b.String()
}

// fakeUpstreamRedis records whether the real password (not the handle) arrived.
func fakeUpstreamRedis(t *testing.T, sawReal *bool, mu *sync.Mutex) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf) // the broker's AUTH
		s := string(buf[:n])
		mu.Lock()
		*sawReal = strings.Contains(s, "realpass") && !strings.Contains(s, "poddle_")
		mu.Unlock()
		_, _ = conn.Write([]byte("+OK\r\n"))
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
			_, _ = conn.Write([]byte("+PONG\r\n"))
		}
	}()
	return ln.Addr().String()
}

func TestDaemon_L4Redis_SwapsHandle(t *testing.T) {
	var mu sync.Mutex
	var sawReal bool
	upAddr := fakeUpstreamRedis(t, &sawReal, &mu)

	d := New(broker.NewBroker(), nil)
	if _, err := d.Start("0.0.0.0:0", "redact", "127.0.0.1:0", "", ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })

	// Register a redis datastore credential and issue a handle for it.
	credID, err := d.broker.Store(broker.Credential{Vendor: "redis", BaseURL: "redis://:realpass@" + upAddr})
	if err != nil {
		t.Fatal(err)
	}
	h, err := d.broker.IssueHandle(credID, "box", 0)
	if err != nil {
		t.Fatal(err)
	}

	// A "pod" connects to the daemon's L4 Redis port with the handle as password.
	pod, err := net.Dial("tcp", d.l4RedisAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer pod.Close()
	if _, err := pod.Write([]byte(resp("AUTH", h.Value))); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(pod).ReadString('\n')
	if strings.TrimSpace(line) != "+OK" {
		t.Fatalf("auth reply = %q, want +OK", line)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawReal {
		t.Error("upstream did not see the real password, or the handle leaked")
	}
}

func TestDaemon_Resolve_RewritesLoopbackDatastore(t *testing.T) {
	// A locked pod's local datastore at 127.0.0.1 must be dialed at the host
	// route from the containerized broker, with the real credential intact. When
	// the rewrite is disabled (bare-host broker / tests), the address is untouched.
	store := func(loopback string) (l4Addr, l4Pass string) {
		d := New(broker.NewBroker(), nil)
		d.SetLoopbackHost(loopback)
		credID, err := d.broker.Store(broker.Credential{Vendor: "redis", BaseURL: "redis://:realpass@127.0.0.1:6379"})
		if err != nil {
			t.Fatal(err)
		}
		h, err := d.broker.IssueHandle(credID, "box", 0)
		if err != nil {
			t.Fatal(err)
		}
		tgt, err := d.Resolve(h.Value)
		if err != nil {
			t.Fatal(err)
		}
		return tgt.Addr, tgt.Pass
	}

	addr, pass := store("host.containers.internal")
	if addr != "host.containers.internal:6379" {
		t.Errorf("loopback datastore dialed %q, want host.containers.internal:6379", addr)
	}
	if pass != "realpass" {
		t.Errorf("real password must survive the rewrite, got %q", pass)
	}
	if addr, _ := store(""); addr != "127.0.0.1:6379" {
		t.Errorf("with the rewrite disabled, the address must be untouched, got %q", addr)
	}
}

func TestDaemon_RevokePod(t *testing.T) {
	srv, fb := testServer(t)
	h1 := issue(t, srv.URL, "box")
	h2 := issue(t, srv.URL, "box")

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/pods/box", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %v status=%v", err, resp.StatusCode)
	}
	if len(fb.revoked) != 2 || fb.revoked[0] != h1 || fb.revoked[1] != h2 {
		t.Errorf("revoked = %v, want [%s %s]", fb.revoked, h1, h2)
	}
	// A second delete is a no-op (pod already gone).
	resp2, _ := http.DefaultClient.Do(req)
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("second delete status = %d", resp2.StatusCode)
	}
	if len(fb.revoked) != 2 {
		t.Errorf("second delete should revoke nothing, got %v", fb.revoked)
	}
}
