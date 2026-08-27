package poddled

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/datadir-lab/poddle/src/internal/broker"
)

// TestFreshAuditGate_DeniesRealEgress is the end-to-end proof of the fail-closed
// enforcement headline: with the gate on, a pod's REAL egress through the broker's
// forward proxy is denied (403) until the audit is acked via POST /audit/ack, then
// allowed (200), and denied again once the ack ages past the staleness window.
//
// Unlike TestFreshAuditGate (which calls Daemon.Check directly), this drives an
// actual proxied HTTP request through broker.ForwardProxy — the same handler and
// the same Check call site (forward.go) that Start wires in production — so it
// exercises the gate on the real egress path, not just the decision function.
func TestFreshAuditGate_DeniesRealEgress(t *testing.T) {
	// The upstream the pod tries to reach.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	upHost := upstream.Listener.Addr().String() // 127.0.0.1:PORT

	// A real daemon with auditing off (nil), the gate ON, and a short staleness.
	// White-box (package poddled) so we can arm the gate without the env dance.
	d := New(broker.NewBroker(), nil)
	d.requireFreshAudit = true
	d.maxStaleness = time.Minute

	// The control API (POST /pods/{pod}/egress, POST /audit/ack).
	ctrl := httptest.NewServer(d.Handler())
	defer ctrl.Close()

	// The egress forward proxy, backed by the daemon as PolicyChecker + Auditor —
	// exactly as Daemon.Start wires it (daemon.go: broker.NewForwardProxy(d, d)).
	proxy := httptest.NewServer(broker.NewForwardProxy(d, d))
	defer proxy.Close()

	// Register an egress token for the pod via the real control endpoint; this is
	// what maps the proxy's token back to the pod (handlePod[token] = pod). We bind
	// NO policy, so the pod is default-allow — a 403 is therefore unambiguously the
	// fresh-audit gate, not a policy denial.
	token := postEgressToken(t, ctrl.URL, "pod-1")

	// Drive a real egress GET through the proxy, presenting the egress token as the
	// Basic-auth proxy credential (proxyAuthToken reads the username half). Returns
	// the status and body so a denial can be attributed to the gate by its reason.
	doEgress := func() (int, string) {
		t.Helper()
		pu, err := url.Parse(proxy.URL)
		if err != nil {
			t.Fatalf("parse proxy url: %v", err)
		}
		pu.User = url.UserPassword(token, "") // -> Proxy-Authorization: Basic base64(token:)
		client := &http.Client{
			Timeout:   5 * time.Second,
			Transport: &http.Transport{Proxy: http.ProxyURL(pu)},
		}
		resp, err := client.Get("http://" + upHost + "/")
		if err != nil {
			t.Fatalf("egress request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	// Gate ON, never acked -> egress denied (fail closed), and the reason confirms
	// it is the freshness gate rather than some other 403.
	if code, body := doEgress(); code != http.StatusForbidden {
		t.Fatalf("stale gate: egress status = %d, want 403 (fail-closed)", code)
	} else if !strings.Contains(body, "audit not fresh") {
		t.Fatalf("stale gate: deny body = %q, want it to mention 'audit not fresh'", body)
	}

	// A fresh ack via the real control endpoint -> egress allowed.
	postAck(t, ctrl.URL, 7)
	if code, _ := doEgress(); code != http.StatusOK {
		t.Fatalf("fresh ack: egress status = %d, want 200 (allowed)", code)
	}

	// Age the ack past the staleness window -> denied again.
	d.mu.Lock()
	d.lastAckAt = time.Now().Add(-2 * time.Minute)
	d.mu.Unlock()
	if code, _ := doEgress(); code != http.StatusForbidden {
		t.Fatalf("re-stale gate: egress status = %d, want 403 (fail-closed)", code)
	}
}

// postEgressToken mints a forward-proxy egress token for pod via POST
// /pods/{pod}/egress and returns it.
func postEgressToken(t *testing.T, ctrlURL, pod string) string {
	t.Helper()
	resp, err := http.Post(ctrlURL+"/pods/"+pod+"/egress", "application/json", nil)
	if err != nil {
		t.Fatalf("post egress: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post egress status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode egress token: %v", err)
	}
	if body.Token == "" {
		t.Fatal("egress token is empty")
	}
	return body.Token
}

// postAck reports an audit watermark via POST /audit/ack (the endpoint the cloud
// agent calls), stamping the daemon's freshness clock.
func postAck(t *testing.T, ctrlURL string, ackedThrough int64) {
	t.Helper()
	body, _ := json.Marshal(struct {
		AckedThrough int64 `json:"acked_through"`
	}{ackedThrough})
	resp, err := http.Post(ctrlURL+"/audit/ack", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post ack: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("post ack status = %d, want 204", resp.StatusCode)
	}
}
