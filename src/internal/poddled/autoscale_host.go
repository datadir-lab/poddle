package poddled

import (
	"context"
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"time"

	"github.com/datadir-lab/poddle/src/internal/exec"
	"github.com/datadir-lab/poddle/src/internal/podman"
)

// autoscaleLockPath is the single-instance lock for the host autoscaler: a unix
// socket beside the daemon's control socket. Holding a listener on it IS the
// lock, so only one host autoscaler runs per daemon.
func autoscaleLockPath(socket string) string {
	return filepath.Join(filepath.Dir(socket), "autoscaled.lock")
}

// acquireAutoscaleLock takes the single-instance host-autoscaler lock. It
// returns a live listener (held=true) when this caller now owns the lock — the
// caller must keep the listener open and close it on exit — or (nil, false)
// when another live instance already holds it. A stale lock file left by a
// crashed instance (nothing listening) is cleared and reclaimed.
func acquireAutoscaleLock(socket string) (ln net.Listener, held bool, err error) {
	path := autoscaleLockPath(socket)
	if ln, err = net.Listen("unix", path); err == nil {
		return ln, true, nil
	}
	// The path is occupied. A live holder answers a dial; otherwise it is a
	// stale socket file (crashed instance) we can remove and reclaim.
	if conn, derr := net.Dial("unix", path); derr == nil {
		_ = conn.Close()
		return nil, false, nil
	}
	_ = os.Remove(path)
	if ln, err = net.Listen("unix", path); err != nil {
		return nil, false, err
	}
	return ln, true, nil
}

// RunHostAutoscaler runs the reactive memory-grow autoscaler on the HOST — where
// podman and the poddle binary live — because the pod-lifecycle work (shelling
// `podman ps` / `poddle move`) can no longer run inside the distroless broker
// container. Its activity is pushed to the daemon at socket so `daemon status`
// and the audit log still surface it. A single-instance lock keeps only one
// running per daemon; a second call returns nil immediately. Blocks until ctx is
// cancelled.
func RunHostAutoscaler(ctx context.Context, socket string) error {
	ln, held, err := acquireAutoscaleLock(socket)
	if err != nil {
		return err
	}
	if !held {
		return nil // another host autoscaler already owns the lock
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(autoscaleLockPath(socket))
	}()

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
// when one is not. A live instance answers a dial on its lock socket, so once
// the autoscaler is up this is a cheap no-op.
func EnsureHostAutoscaler(socket string) {
	if conn, err := net.Dial("unix", autoscaleLockPath(socket)); err == nil {
		_ = conn.Close()
		return // already running
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
