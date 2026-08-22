package poddled

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

// skipNoFlock skips on Windows, where the lock uses the best-effort O_EXCL
// fallback rather than flock (the poddled lock tests run on linux/mac and CI).
func skipNoFlock(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("flock lock runs on linux/mac (and in CI)")
	}
}

func TestAcquireAutoscaleLock_SingleInstance(t *testing.T) {
	skipNoFlock(t)
	sock := tmpSock(t)

	release, held, err := acquireAutoscaleLock(sock)
	if err != nil || !held || release == nil {
		t.Fatalf("first acquire: held=%v release=%v err=%v; want a held lock", held, release != nil, err)
	}
	defer release()

	// A second acquire while the first still holds the lock must not take it.
	release2, held2, err := acquireAutoscaleLock(sock)
	if err != nil {
		t.Fatalf("second acquire error: %v", err)
	}
	if held2 {
		if release2 != nil {
			release2()
		}
		t.Fatal("second acquire took the lock while the first held it; want held=false")
	}
}

func TestAcquireAutoscaleLock_ReclaimsAfterRelease(t *testing.T) {
	skipNoFlock(t)
	sock := tmpSock(t)

	release, held, err := acquireAutoscaleLock(sock)
	if err != nil || !held {
		t.Fatalf("first acquire: held=%v err=%v", held, err)
	}
	release()

	// Once released, the lock is free again.
	release2, held2, err := acquireAutoscaleLock(sock)
	if err != nil || !held2 || release2 == nil {
		t.Fatalf("reacquire after release: held=%v err=%v", held2, err)
	}
	release2()
}

func TestAcquireAutoscaleLock_TakesOverStaleLockFile(t *testing.T) {
	skipNoFlock(t)
	sock := tmpSock(t)

	// A leftover lock file with no live holder (a crashed instance) carries no
	// flock, so acquire must lock it and take ownership — not fail.
	if err := os.WriteFile(autoscaleLockPath(sock), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, held, err := acquireAutoscaleLock(sock)
	if err != nil || !held || release == nil {
		t.Fatalf("acquire over a stale lock file: held=%v err=%v", held, err)
	}
	release()
}

// A second RunHostAutoscaler for a socket whose lock is already held must return
// promptly (nil) without starting the loop — the single-instance guard.
func TestRunHostAutoscaler_SecondInstanceReturnsPromptly(t *testing.T) {
	skipNoFlock(t)
	sock := tmpSock(t)

	release, held, err := acquireAutoscaleLock(sock) // simulate a first instance holding it
	if err != nil || !held {
		t.Fatalf("hold lock: held=%v err=%v", held, err)
	}
	defer release()

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

func tmpSock(t *testing.T) string {
	t.Helper()
	return t.TempDir() + string(os.PathSeparator) + "poddled.sock"
}
