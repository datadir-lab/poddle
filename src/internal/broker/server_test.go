package broker

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestServer_ServeRoundTripStop(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "realtok", BaseURL: up.URL})

	s := NewServer(g)
	addr, err := s.Serve("127.0.0.1:0")
	if err != nil {
		t.Fatalf("serve: %v", err)
	}

	// Keep-alives off so the post-Stop request dials fresh (and is refused).
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}

	// One request round-trips through the real server to the fake upstream.
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer "+handle)
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
		t.Errorf("upstream Authorization = %q, want %q", rec.auth, "Bearer realtok")
	}

	// After Stop, the address no longer accepts.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := client.Get("http://" + addr + "/v1/messages"); err == nil {
		t.Errorf("request after Stop should fail, got nil error")
	}
}

func TestServer_StopWithoutServe(t *testing.T) {
	s := NewServer(nil)
	if err := s.Stop(context.Background()); err != nil {
		t.Errorf("Stop on un-served server = %v, want nil", err)
	}
}
