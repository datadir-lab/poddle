package broker

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRoundTrip_StreamsSSE proves the gateway streams a Server-Sent-Events
// response chunk-by-chunk instead of buffering it — the upstream is blocked
// from sending the second event until the client has already received the
// first through the gateway. (Claude Code depends on streaming responses.)
func TestRoundTrip_StreamsSSE(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	closeRelease := func() { once.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("upstream ResponseWriter is not a Flusher")
			return
		}
		fmt.Fprint(w, "data: one\n\n")
		fl.Flush()
		<-release // hold the second event until the test releases it
		fmt.Fprint(w, "data: two\n\n")
		fl.Flush()
	}))
	t.Cleanup(up.Close)

	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "tok", BaseURL: up.URL})
	gw := serve(t, g)

	req, _ := http.NewRequest(http.MethodGet, gw.URL+"/v1/stream", nil)
	req.Header.Set("Authorization", "Bearer "+handle)
	client := &http.Client{Timeout: 5 * time.Second} // a buffering proxy would deadlock → timeout → fail
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	br := bufio.NewReader(resp.Body)
	if first, err := readSSEData(br); err != nil || first != "one" {
		t.Fatalf("first event = %q, err %v; want one", first, err)
	}
	// Getting "one" while the upstream is still blocked proves streaming.
	closeRelease()
	if second, err := readSSEData(br); err != nil || second != "two" {
		t.Fatalf("second event = %q, err %v; want two", second, err)
	}
}

// TestRoundTrip_MultiTenantIsolation runs two tenants through one gateway and
// asserts each handle reaches only its own upstream with only its own secret.
func TestRoundTrip_MultiTenantIsolation(t *testing.T) {
	upA, recA := upstreamRecording(t)
	upB, recB := upstreamRecording(t)

	v := NewVault()
	h := NewHandles(v)
	idA, _ := v.Store("tenant-a", Credential{Mode: ModeSubscription, Secret: "secretA", BaseURL: upA.URL})
	idB, _ := v.Store("tenant-b", Credential{Mode: ModeSubscription, Secret: "secretB", BaseURL: upB.URL})
	hA, _ := h.IssueHandle("tenant-a", idA, "boxA", time.Hour)
	hB, _ := h.IssueHandle("tenant-b", idB, "boxB", time.Hour)
	gw := serve(t, NewGateway(h))

	if code := do(t, gw, hA.Value, http.MethodGet, "/a", nil); code != http.StatusOK {
		t.Fatalf("tenant-a status = %d, want 200", code)
	}
	if code := do(t, gw, hB.Value, http.MethodGet, "/b", nil); code != http.StatusOK {
		t.Fatalf("tenant-b status = %d, want 200", code)
	}

	if recA.auth != "Bearer secretA" || recA.path != "/a" {
		t.Errorf("upstream A saw auth=%q path=%q, want Bearer secretA /a", recA.auth, recA.path)
	}
	if recB.auth != "Bearer secretB" || recB.path != "/b" {
		t.Errorf("upstream B saw auth=%q path=%q, want Bearer secretB /b", recB.auth, recB.path)
	}
	if strings.Contains(recA.auth, "secretB") {
		t.Errorf("tenant-b secret leaked to upstream A: %q", recA.auth)
	}
	if strings.Contains(recB.auth, "secretA") {
		t.Errorf("tenant-a secret leaked to upstream B: %q", recB.auth)
	}
}

// readSSEData reads lines until it finds a "data: " line and returns its value.
func readSSEData(br *bufio.Reader) (string, error) {
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		if v, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data: "); ok {
			return v, nil
		}
	}
}
