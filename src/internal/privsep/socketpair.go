//go:build linux

package privsep

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// This package is the pure process-separation MECHANISM for the broker's Tier-2
// privilege split: create a socketpair, re-exec self as a keeper child with one
// end inherited at fd 3, and supervise it. It carries NO protocol — the broker
// package owns the keeper RPC (broker.serveKeeper / socketKeeperClient) over the
// *net.UnixConn this returns. Keeping it protocol-free means privsep imports
// nothing of broker (so broker can import privsep for Spawn with no cycle), and
// the whole thing stays a small, auditable, Linux-only surface. See
// docs/design/broker-privilege-separation.md.

const (
	// keeperEnvVar marks a re-exec'd process as the privsep keeper child. The
	// re-exec entrypoint checks it (IsKeeperMode) before normal startup and, if
	// set, serves the keeper against the inherited socketpair instead of the front.
	keeperEnvVar = "PODDLE_PRIVSEP_KEEPER"

	// keeperFDNum is where ExtraFiles[0] lands in the child: fds 0/1/2 are
	// stdin/stdout/stderr, so the first inherited extra file is fd 3.
	keeperFDNum = 3
)

// Spawn creates an AF_UNIX SOCK_STREAM socketpair, re-execs THIS binary in keeper
// mode with one end inherited at fd 3 (via ExtraFiles) and PODDLE_PRIVSEP_KEEPER=1
// in its environment, and returns the front-side conn plus the already-started
// child *exec.Cmd. The caller supervises cmd (see Supervise) and talks to the
// keeper over the returned conn (the broker wraps it in a socketKeeperClient).
//
// args are passed to the re-exec'd binary (e.g. a subcommand marker for a
// production main); the env var is what the entrypoint actually keys on.
//
// Topology: whichever process calls Spawn becomes the PARENT. poddle's production
// front (poddled) calls Spawn, so the FRONT forks the KEEPER child; the child
// holds the vault, the front holds only the socket.
func Spawn(args ...string) (*net.UnixConn, *exec.Cmd, error) {
	// SOCK_CLOEXEC so neither end leaks into unrelated children on exec; the keeper
	// end is still delivered to the child because ExtraFiles dup2's it into place
	// (fd 3), and dup2 clears close-on-exec on the target.
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("privsep: socketpair: %w", err)
	}
	frontFile := os.NewFile(uintptr(fds[0]), "privsep-front")
	keeperFile := os.NewFile(uintptr(fds[1]), "privsep-keeper")

	frontConn, err := unixConnFromFile(frontFile)
	_ = frontFile.Close() // net.FileConn dups the fd
	if err != nil {
		_ = keeperFile.Close()
		return nil, nil, err
	}

	self, err := os.Executable()
	if err != nil {
		_ = keeperFile.Close()
		_ = frontConn.Close()
		return nil, nil, fmt.Errorf("privsep: locate self: %w", err)
	}

	cmd := exec.Command(self, args...)
	cmd.Env = append(os.Environ(), keeperEnvVar+"=1")
	cmd.ExtraFiles = []*os.File{keeperFile} // -> child fd 3
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Pdeathsig is defense in depth: if the parent (front) dies, the kernel SIGKILLs
	// the keeper so no orphaned secret-holder survives even if it were blocked
	// somewhere other than the socket read. The primary death signal is still the
	// socketpair EOF the serve loop observes. (Pdeathsig is thread-scoped in Go, so
	// it's a backstop, not the mechanism.)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}

	if err := cmd.Start(); err != nil {
		_ = keeperFile.Close()
		_ = frontConn.Close()
		return nil, nil, fmt.Errorf("privsep: start keeper: %w", err)
	}
	// The child inherited its own dup of the keeper end; close the parent's copy so
	// that when the child exits the front's read sees a clean EOF (and vice versa).
	// Keeping it open here would defeat EOF-based death detection.
	_ = keeperFile.Close()

	return frontConn, cmd, nil
}

// Supervise waits on the keeper child in a goroutine and returns a channel that
// delivers the child's exit result exactly once. If the keeper dies, the front's
// next keeper call sees a socketpair read error (fail closed) — the front never
// proceeds without a live vault. The caller must not also call cmd.Wait.
func Supervise(cmd *exec.Cmd) <-chan error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return done
}

// IsKeeperMode reports whether this process was re-exec'd as the privsep keeper
// (PODDLE_PRIVSEP_KEEPER=1). The re-exec entrypoint checks this before normal
// startup and, if true, serves the keeper (broker.RunKeeperProcess) instead of the
// front.
func IsKeeperMode() bool { return os.Getenv(keeperEnvVar) == "1" }

// KeeperConn returns the inherited socketpair end (fd 3) as a *net.UnixConn. Valid
// only in a process for which IsKeeperMode() is true; the broker serves its keeper
// RPC over the returned conn.
func KeeperConn() (*net.UnixConn, error) {
	f := os.NewFile(uintptr(keeperFDNum), "privsep-keeper")
	if f == nil {
		return nil, errors.New("privsep: keeper fd 3 was not inherited")
	}
	conn, err := unixConnFromFile(f)
	_ = f.Close() // net.FileConn dups
	return conn, err
}

// unixConnFromFile wraps an fd-backed *os.File as a *net.UnixConn, asserting the
// concrete type net.FileConn returns for an AF_UNIX socket.
func unixConnFromFile(f *os.File) (*net.UnixConn, error) {
	c, err := net.FileConn(f)
	if err != nil {
		return nil, fmt.Errorf("privsep: FileConn: %w", err)
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()
		return nil, fmt.Errorf("privsep: fd is %T, not *net.UnixConn", c)
	}
	return uc, nil
}
