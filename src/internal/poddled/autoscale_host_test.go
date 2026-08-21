package poddled

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// skipNoUnixSocket skips on Windows, where these tests bind a unix-socket lock
// (the poddled unix-socket tests run on linux/mac and in CI).
func skipNoUnixSocket(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix socket lock runs on linux/mac (and in CI)")
	}
}

func TestAcquireAutoscaleLock_SingleInstance(t *testing.T) {
	skipNoUnixSocket(t)
	sock := filepath.Join(t.TempDir(), "poddled.sock")

	ln, held, err := acquireAutoscaleLock(sock)
	if err != nil || !held || ln == nil {
		t.Fatalf("first acquire: held=%v ln=%v err=%v; want a held listener", held, ln, err)
	}
	defer ln.Close()

	// A second acquire while the first still holds the lock must not take it.
	ln2, held2, err := acquireAutoscaleLock(sock)
	if err != nil {
		t.Fatalf("second acquire error: %v", err)
	}
	if held2 || ln2 != nil {
		if ln2 != nil {
			ln2.Close()
		}
		t.Fatal("second acquire took the lock while the first held it; want held=false")
	}
}

func TestAcquireAutoscaleLock_ReclaimsAfterRelease(t *testing.T) {
	skipNoUnixSocket(t)
	sock := filepath.Join(t.TempDir(), "poddled.sock")

	ln, held, err := acquireAutoscaleLock(sock)
	if err != nil || !held {
		t.Fatalf("first acquire: held=%v err=%v", held, err)
	}
	_ = ln.Close()

	// Once released, the lock is free again.
	ln2, held2, err := acquireAutoscaleLock(sock)
	if err != nil || !held2 || ln2 == nil {
		t.Fatalf("reacquire after release: held=%v err=%v", held2, err)
	}
	ln2.Close()
}

func TestAcquireAutoscaleLock_RemovesStaleSocketFile(t *testing.T) {
	skipNoUnixSocket(t)
	sock := filepath.Join(t.TempDir(), "poddled.sock")

	// A leftover file at the lock path with nothing listening is stale (crashed
	// instance): acquire must clear it and take the lock, not fail.
	if err := os.WriteFile(autoscaleLockPath(sock), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ln, held, err := acquireAutoscaleLock(sock)
	if err != nil || !held || ln == nil {
		t.Fatalf("acquire over a stale file: held=%v err=%v", held, err)
	}
	ln.Close()
}

// A second RunHostAutoscaler for a socket whose lock is already held must return
// promptly (nil) without starting the loop — the single-instance guard.
func TestRunHostAutoscaler_SecondInstanceReturnsPromptly(t *testing.T) {
	skipNoUnixSocket(t)
	sock := filepath.Join(t.TempDir(), "poddled.sock")

	ln, held, err := acquireAutoscaleLock(sock) // simulate a first instance holding it
	if err != nil || !held {
		t.Fatalf("hold lock: held=%v err=%v", held, err)
	}
	defer ln.Close()

	done := make(chan error, 1)
	go func() { done <- RunHostAutoscaler(context.Background(), sock) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second RunHostAutoscaler = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second RunHostAutoscaler did not return; it should no-op when another instance holds the lock")
	}
}
