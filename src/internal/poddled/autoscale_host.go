package poddled

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"time"

	"github.com/datadir-lab/poddle/src/internal/exec"
	"github.com/datadir-lab/poddle/src/internal/podman"
)

// autoscaleLockPath is the single-instance lock file for the host autoscaler,
// beside the daemon's control socket.
func autoscaleLockPath(socket string) string {
	return filepath.Join(filepath.Dir(socket), "autoscaled.lock")
}

// acquireAutoscaleLock takes the single-instance host-autoscaler lock. It
// returns (release, true, nil) when this caller now owns the lock — the caller
// must call release on exit — or (nil, false, nil) when another live instance
// already holds it. The lock is an advisory (flock) file lock the kernel drops
// when the holder exits, even on a crash, so there is no stale lock to reclaim
// and no reclaim TOCTOU (two starts can never both win).
func acquireAutoscaleLock(socket string) (release func(), held bool, err error) {
	return tryLockFile(autoscaleLockPath(socket))
}

// RunHostAutoscaler runs the reactive memory-grow autoscaler on the HOST — where
// podman and the poddle binary live — because the pod-lifecycle work (shelling
// `podman ps` / `poddle move`) can no longer run inside the distroless broker
// container. Its activity is pushed to the daemon at socket so `daemon status`
// and the audit log still surface it. A single-instance lock keeps only one
// running per daemon; a second call returns nil immediately. Blocks until ctx is
// cancelled.
func RunHostAutoscaler(ctx context.Context, socket string) error {
	release, held, err := acquireAutoscaleLock(socket)
	if err != nil {
		return err
	}
	if !held {
		return nil // another host autoscaler already owns the lock
	}
	defer release()

	client := NewClient(socket)
	as := &Autoscaler{
		Interval: autoscaleInterval(), Cooldown: 60 * time.Second,
		HighWater: 85, Sustain: 3,
		Stats: productionStats(podman.New(exec.OS{}, "")), // host podman => empty url
		Move:  selfMover(),
		Log:   func(format string, args ...any) { _ = client.PushEvent(fmt.Sprintf(format, args...)) },
	}
	as.Run(ctx)
	return nil
}

// EnsureHostAutoscaler makes sure a host autoscaler is running for the daemon at
// socket, spawning `poddle daemon autoscaled` detached (surviving the CLI exit)
// when one is not. If the lock is already held, an autoscaler is running and
// this is a no-op; otherwise we release our probe and spawn one (which re-takes
// the lock — flock guarantees a single winner even if two spawns race).
func EnsureHostAutoscaler(socket string) {
	if release, held, err := acquireAutoscaleLock(socket); err == nil {
		if !held {
			return // another instance holds the lock -> already running
		}
		release() // free it for the spawned process to take
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := osexec.Command(self, "daemon", "autoscaled", "--socket", socket)
	cmd.SysProcAttr = detachAttrs()
	if devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	}
	if err := cmd.Start(); err != nil {
		return
	}
	_ = cmd.Process.Release() // it outlives this CLI process
}
