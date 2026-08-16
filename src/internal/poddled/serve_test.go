package poddled

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSocketPath_HonorsXDG(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join("tmp", "run"))
	got := SocketPath()
	want := filepath.Join("tmp", "run", "poddle", "poddled.sock")
	if got != want {
		t.Errorf("SocketPath = %q, want %q", got, want)
	}
}

func TestServe_HealthOverUDS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket round-trip runs on linux/mac (and in CI)")
	}
	sock := filepath.Join(t.TempDir(), "poddled.sock")
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- Serve(ctx, sock, "0.0.0.0:0", "redact", "", "") }()

	// Wait for the socket to appear.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket never appeared")
		}
		time.Sleep(20 * time.Millisecond)
	}

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	resp, err := client.Get("http://unix/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("health over uds: %v status=%v", err, resp)
	}
	resp.Body.Close()

	cancel()
	if err := <-errc; err != nil {
		t.Errorf("Serve returned %v", err)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket should be cleaned up on stop")
	}
}
