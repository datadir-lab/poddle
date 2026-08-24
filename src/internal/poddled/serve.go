package poddled

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/datadir-lab/poddle/src/internal/audit"
	"github.com/datadir-lab/poddle/src/internal/broker"
)

// AuditDBPath is where the daemon keeps its audit log: under XDG_STATE_HOME when
// set (the right home for state), else the user config dir, else temp.
func AuditDBPath() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		if cfg, err := os.UserConfigDir(); err == nil {
			dir = cfg
		} else {
			dir = os.TempDir()
		}
	}
	return filepath.Join(dir, "poddle", "audit.db")
}

// SocketPath is the control-socket path. PODDLE_SOCKET overrides it outright (so
// a caller can isolate poddled's socket without repointing XDG_RUNTIME_DIR,
// which rootless podman also relies on); otherwise it lives under
// XDG_RUNTIME_DIR when set (the right home for runtime sockets), else the user
// config dir, else temp.
func SocketPath() string {
	if s := os.Getenv("PODDLE_SOCKET"); s != "" {
		return s
	}
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
func Serve(ctx context.Context, sockPath, gatewayBind, egress, l4RedisBind, l4PostgresBind, forwardBind string) error {
	dbPath := AuditDBPath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return err
	}
	store, err := audit.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}

	d := New(broker.NewBroker(), store)
	// A containerized broker sets PODDLE_LOOPBACK_HOST=host.containers.internal so
	// a pod's loopback upstream (a local Postgres/Redis, or a local HTTP service)
	// reaches the host, not the broker container's own empty loopback. Unset on a
	// bare-host broker, where loopback already means the host.
	d.SetLoopbackHost(os.Getenv("PODDLE_LOOPBACK_HOST"))
	if _, err := d.Start(gatewayBind, egress, l4RedisBind, l4PostgresBind, forwardBind); err != nil {
		return err
	}

	// The reactive autoscaler now runs on the HOST (`poddle daemon autoscaled`,
	// auto-started by `up --autoscale`): it shells podman / `poddle move`, which
	// this distroless broker container cannot. It pushes its activity back over
	// the control API (POST /events) so `daemon status` still surfaces it.
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		return err
	}
	_ = os.Remove(sockPath) // clear a stale socket from a prior run
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return err
	}
	_ = os.Chmod(sockPath, 0o600) // owner-only: the control API is the authz boundary

	srv := &http.Server{Handler: d.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
		// Shutdown must run to completion even though ctx is now cancelled;
		// WithoutCancel keeps ctx's values but drops its cancellation.
		_ = d.Stop(context.WithoutCancel(ctx))
		_ = store.Close()
		_ = os.Remove(sockPath)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
