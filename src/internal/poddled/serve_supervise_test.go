package poddled

import (
	"context"
	"errors"
	"testing"
)

// TestSuperviseKeeper_UnexpectedDeath: while the daemon is running (ctx live), an
// unexpected keeper death cancels the serve context (fail closed) and reports the
// death so Serve can exit non-zero for its supervisor to restart.
func TestSuperviseKeeper_UnexpectedDeath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keeperDeath := make(chan error, 1)
	keeperDied := make(chan error, 1)
	keeperDeath <- errors.New("keeper OOM")

	superviseKeeper(ctx, cancel, keeperDeath, keeperDied)

	if ctx.Err() == nil {
		t.Error("unexpected keeper death should have cancelled the serve context")
	}
	select {
	case err := <-keeperDied:
		if err == nil {
			t.Error("keeperDied should carry a non-nil error")
		}
	default:
		t.Error("unexpected keeper death should report on keeperDied (so Serve exits non-zero)")
	}
}

// TestSuperviseKeeper_NormalShutdownQuiet: on a normal shutdown (ctx already
// cancelled, keeper being closed), supervise exits via the ctx.Done arm without
// reporting a death — so Serve exits 0, not a spurious failure.
func TestSuperviseKeeper_NormalShutdownQuiet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // graceful shutdown already underway
	keeperDeath := make(chan error, 1)
	keeperDied := make(chan error, 1)

	superviseKeeper(ctx, cancel, keeperDeath, keeperDied)

	select {
	case <-keeperDied:
		t.Error("a normal shutdown must not report a keeper death")
	default:
	}
}

// TestSuperviseKeeper_DeathDuringShutdownSuppressed: even if the keeper's death and
// the shutdown race (both select arms ready), the ctx.Err guard suppresses a
// spurious fail-closed report.
func TestSuperviseKeeper_DeathDuringShutdownSuppressed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	keeperDeath := make(chan error, 1)
	keeperDeath <- errors.New("keeper closed by shutdown")
	keeperDied := make(chan error, 1)

	superviseKeeper(ctx, cancel, keeperDeath, keeperDied)

	select {
	case <-keeperDied:
		t.Error("a keeper death during a normal shutdown must be suppressed")
	default:
	}
}
