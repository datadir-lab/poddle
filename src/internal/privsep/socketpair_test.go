//go:build linux

package privsep

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"
)

// echoMsg is the fixed payload the re-exec'd keeper child echoes back. The privsep
// package is protocol-free, so the test uses a raw fixed-size echo to prove the
// mechanism (socketpair + fd-inherit + re-exec + supervision); the REAL keeper RPC
// crossing a real credential is proven in the broker package's linux integration
// test, which serves broker.serveKeeper over this same transport.
const echoMsg = "privsep-socketpair-mechanism-roundtrip-proof"

// TestMain implements the standard Go re-exec pattern: when this binary is launched
// with PODDLE_PRIVSEP_KEEPER=1 (which Spawn sets on the child), it does NOT run the
// suite — it attaches to the inherited socketpair (fd 3) via KeeperConn, echoes one
// message, and exits. So Spawn() re-execs THIS test binary as the keeper child and
// a round-trip crosses two real processes.
func TestMain(m *testing.M) {
	if IsKeeperMode() {
		conn, err := KeeperConn()
		if err != nil {
			os.Exit(1)
		}
		buf := make([]byte, len(echoMsg))
		if _, err := io.ReadFull(conn, buf); err != nil {
			_ = conn.Close()
			os.Exit(1)
		}
		_, werr := conn.Write(buf)
		_ = conn.Close()
		if werr != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestPrivsep_SocketpairRoundTrip is the mechanism proof: spawn the keeper as a
// separate process, round-trip a payload over the inherited socketpair, and assert
// it comes back byte-identical — proving fd-passing + re-exec + socketpair work
// (and, under the broker's locked container in CI, that Tier-1 hardening doesn't
// break them).
func TestPrivsep_SocketpairRoundTrip(t *testing.T) {
	conn, cmd, err := Spawn()
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	death := Supervise(cmd)
	t.Cleanup(func() { _ = conn.Close(); <-death })

	if _, err := conn.Write([]byte(echoMsg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(echoMsg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(buf, []byte(echoMsg)) {
		t.Errorf("echo = %q, want %q", buf, echoMsg)
	}
}

// TestPrivsep_KeeperDeathClosesFront proves the fail-closed death signal: once the
// keeper process exits, the front's socketpair read sees EOF rather than hanging —
// this is how the broker detects a dead keeper and fails closed.
func TestPrivsep_KeeperDeathClosesFront(t *testing.T) {
	conn, cmd, err := Spawn()
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	death := Supervise(cmd)
	t.Cleanup(func() { _ = conn.Close() })

	// One echo makes the keeper's TestMain complete and exit.
	if _, err := conn.Write([]byte(echoMsg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := io.ReadFull(conn, make([]byte, len(echoMsg))); err != nil {
		t.Fatalf("read: %v", err)
	}
	<-death // keeper has exited

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Error("front read after keeper exit should see EOF/error, got nil")
	}
}
