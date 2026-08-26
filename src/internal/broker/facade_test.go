package broker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestBroker_StoreIssueResolve(t *testing.T) {
	b := NewBroker()
	cred := Credential{Mode: ModeSubscription, Vendor: "anthropic", Secret: "tok", BaseURL: "https://api.anthropic.com"}

	credID, err := b.Store(cred)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	h, err := b.IssueHandle(credID, "box", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	_, got, err := b.handles.Resolve(h.Value) // white-box: the handle maps back to the cred
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != cred {
		t.Errorf("resolved %+v, want %+v", got, cred)
	}
}

func TestBroker_Revoke(t *testing.T) {
	b := NewBroker()
	credID, _ := b.Store(Credential{Secret: "x"})
	h, _ := b.IssueHandle(credID, "box", time.Hour)

	b.Revoke(h.Value)
	if _, _, err := b.handles.Resolve(h.Value); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoked handle should be ErrNotFound, got %v", err)
	}
}

func TestBroker_AddrEmptyUntilServe(t *testing.T) {
	b := NewBroker()
	if b.Addr() != "" {
		t.Errorf("Addr before serve = %q, want empty", b.Addr())
	}
	addr, err := b.Serve("127.0.0.1:0")
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background()) })
	if addr == "" || b.Addr() != addr {
		t.Errorf("Addr after serve = %q, Serve returned %q", b.Addr(), addr)
	}
}

// TestBroker_StoreClearsNeedsReauthFlag covers §T3: once a connection is
// flagged needs-reauth (a prior refresh failed), the host resolving it by
// re-storing a fresh credential for the same WriteBackKey clears the flag.
func TestBroker_StoreClearsNeedsReauthFlag(t *testing.T) {
	b := NewBroker()
	// White-box: flag "gh" directly, standing in for a prior failed refresh
	// (TestGateway_FlagsNeedsReauthOnFailure covers how the flag gets set).
	b.server.gw.reauthMu.Lock()
	b.server.gw.needsReauth["gh"] = true
	b.server.gw.reauthMu.Unlock()

	if _, err := b.Store(Credential{Mode: ModeOAuthBearer, WriteBackKey: "gh", Secret: "x", RefreshToken: "y"}); err != nil {
		t.Fatalf("store: %v", err)
	}
	for _, k := range b.NeedsReauth() {
		if k == "gh" {
			t.Errorf("NeedsReauth() = %v, want it to no longer contain %q after Store", b.NeedsReauth(), "gh")
		}
	}
}

func TestBroker_ServeRoundTripStop(t *testing.T) {
	up, rec := upstreamRecording(t)
	b := NewBroker()
	credID, _ := b.Store(Credential{Mode: ModeSubscription, Secret: "realtok", BaseURL: up.URL})
	h, _ := b.IssueHandle(credID, "box", time.Hour)

	addr, err := b.Serve("127.0.0.1:0")
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}

	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer "+h.Value)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if rec.auth != "Bearer realtok" {
		t.Errorf("upstream Authorization = %q, want Bearer realtok", rec.auth)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := b.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := client.Get("http://" + addr + "/v1/messages"); err == nil {
		t.Errorf("request after Stop should fail")
	}
}
