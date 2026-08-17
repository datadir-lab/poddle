package poddled

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
	"git.dev.datadir.co/datadir/poddle/src/internal/exec"
	"git.dev.datadir.co/datadir/poddle/src/internal/podman"
)

// SocketPath is the default control-socket path: under XDG_RUNTIME_DIR when set
// (the right home for runtime sockets), else the user config dir, else temp.
func SocketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		if cfg, err := os.UserConfigDir(); err == nil {
			dir = cfg
		} else {
			dir = os.TempDir()
		}
	}
	return filepath.Join(dir, "poddle", "poddled.sock")
}

// Serve runs the daemon until ctx is cancelled: it binds the injecting HTTP
// gateway (pods reach it over TCP), the L4 Redis listener, and serves the
// control API on an owner-only Unix socket at sockPath. A stale socket is
// replaced.
func Serve(ctx context.Context, sockPath, gatewayBind, egress, l4RedisBind, l4PostgresBind string) error {
	d := New(broker.NewBroker())
	if _, err := d.Start(gatewayBind, egress, l4RedisBind, l4PostgresBind); err != nil {
		return err
	}

	// Reactive autoscaler: watch opted-in pods and grow headless ones under
	// sustained memory pressure (warn interactive ones). Label-gated, so it is a
	// no-op unless a pod opted in with --autoscale. Its activity is recorded as
	// daemon events surfaced by `poddle daemon status`.
	as := &Autoscaler{
		Interval: autoscaleInterval(), Cooldown: 60 * time.Second,
		HighWater: 85, Sustain: 3,
		Stats: productionStats(podman.New(exec.OS{}, "")),
		Move:  selfMover(),
		Log:   func(format string, args ...any) { d.recordEvent(fmt.Sprintf(format, args...)) },
	}
	go as.Run(ctx)
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		return err
	}
	_ = os.Remove(sockPath) // clear a stale socket from a prior run
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return err
	}
	_ = os.Chmod(sockPath, 0o600) // owner-only: the control API is the authz boundary

	srv := &http.Server{Handler: d.Handler()}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
		_ = d.Stop(context.Background())
		_ = os.Remove(sockPath)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
