package poddled

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/datadir-lab/poddle/src/internal/broker"
)

// startDaemon runs a real daemon over a temp UDS and returns a healthy client.
func startDaemon(t *testing.T) *Client {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix socket client runs on linux/mac (and in CI)")
	}
	sock := filepath.Join(t.TempDir(), "poddled.sock")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = Serve(ctx, sock, "0.0.0.0:0", "redact", "0.0.0.0:0", "", "") }()

	c := NewClient(sock)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c.Health() == nil {
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon never became healthy")
	return nil
}

func TestClient_GatewayIssueRevoke(t *testing.T) {
	c := startDaemon(t)
	addr, err := c.Gateway()
	if err != nil || addr == "" {
		t.Fatalf("gateway: %v %q", err, addr)
	}
	h, err := c.IssueHandle("box", "box", broker.Credential{Mode: broker.ModeSubscription, Secret: "s", BaseURL: "http://x"})
	if err != nil || h == "" {
		t.Fatalf("issue: %v %q", err, h)
	}
	if err := c.RevokePod("box"); err != nil {
		t.Errorf("revoke: %v", err)
	}
}

func TestClient_Status(t *testing.T) {
	c := startDaemon(t)
	if _, err := c.IssueHandle("box", "box", broker.Credential{Mode: broker.ModeSubscription, Secret: "s", BaseURL: "http://x"}); err != nil {
		t.Fatal(err)
	}
	s, err := c.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.Gateway == "" {
		t.Error("status should report a gateway address")
	}
	if s.Pods["box"] != 1 {
		t.Errorf("status pods = %v, want box:1", s.Pods)
	}
}

func TestClient_OAuthMirror(t *testing.T) {
	// Isolate OAuthMirrorDir() (under stateHome()) from any real state on the
	// machine running the test, matching the XDG_STATE_HOME isolation pattern
	// used elsewhere (e.g. TestEnsureRunning_CreatesRunAndStateDirs).
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	c := startDaemon(t)
	out, err := c.OAuthMirror()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("fresh daemon's mirror should be empty, got %v", out)
	}
}

func TestClient_EnsureRunning_AlreadyUp(t *testing.T) {
	c := startDaemon(t)
	if err := c.EnsureRunning(); err != nil {
		t.Errorf("EnsureRunning when already up should no-op, got %v", err)
	}
}
