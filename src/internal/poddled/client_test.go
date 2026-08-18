package poddled

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
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

func TestClient_EnsureRunning_AlreadyUp(t *testing.T) {
	c := startDaemon(t)
	if err := c.EnsureRunning(); err != nil {
		t.Errorf("EnsureRunning when already up should no-op, got %v", err)
	}
}
