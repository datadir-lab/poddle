//go:build linux

package privsep

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/datadir-lab/poddle/src/internal/l4"
)

// testPassword is the fixed SCRAM password the re-exec'd keeper child holds. It
// is a test fixture, never a real secret.
const testPassword = "s3cret-spike-password"

// TestMain implements the standard Go re-exec test pattern: when this binary is
// launched with PODDLE_PRIVSEP_KEEPER=1 (which Spawn sets on the child), it does
// NOT run the test suite — it serves the fixed-password keeper against the
// inherited socketpair (fd 3) and exits. Otherwise it runs the tests normally.
// So Spawn() re-execs THIS test binary as the keeper child, and a round-trip
// crosses two real processes.
func TestMain(m *testing.M) {
	if IsKeeperMode() {
		if err := RunKeeper(NewFixedPasswordKeeper(testPassword)); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestPrivsep_SCRAMProof_CrossesProcessBoundary is the core proof: it spawns the
// keeper as a separate process, does a SCRAMProof round-trip over the
// socketpair, and asserts the returned proof is byte-identical to an in-process
// l4.ComputeSCRAMProof for the same inputs — proving the REAL SCRAM proof
// crossed two real processes, not a stub.
func TestPrivsep_SCRAMProof_CrossesProcessBoundary(t *testing.T) {
	conn, cmd, err := Spawn()
	if err != nil {
		t.Fatalf("Spawn keeper: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Realistic-shaped SCRAM inputs (values are arbitrary — the proof math is
	// deterministic in them; iter is a normal Postgres default).
	salt := []byte{0x01, 0x02, 0x03, 0x04, 's', 'a', 'l', 't', 'y', 'S', 'a', 'l', 't'}
	const iter = 4096
	const authMessage = "n=,r=clientnonce,r=clientnonceSERVERnonce,s=AQIDBHNhbHR5U2FsdA==,i=4096,c=biws,r=clientnonceSERVERnonce"

	got, err := newClient(conn).SCRAMProof("pg-handle", salt, iter, authMessage)
	if err != nil {
		t.Fatalf("cross-process SCRAMProof: %v", err)
	}

	want, err := l4.ComputeSCRAMProof(testPassword, salt, iter, authMessage)
	if err != nil {
		t.Fatalf("in-process ComputeSCRAMProof: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("proof mismatch across the process boundary:\n got=%x\nwant=%x", got, want)
	}
	if len(got) != 32 {
		t.Fatalf("SCRAM-SHA-256 proof must be 32 bytes, got %d", len(got))
	}
}

// TestPrivsep_KeeperError_CrossesBoundary asserts a keeper-side error (an
// out-of-range iteration count, which l4's self-protecting Proof rejects) is
// serialized back to the front as a non-nil error rather than a bogus proof.
func TestPrivsep_KeeperError_CrossesBoundary(t *testing.T) {
	conn, cmd, err := Spawn()
	if err != nil {
		t.Fatalf("Spawn keeper: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	if _, err := newClient(conn).SCRAMProof("pg-handle", []byte("saltsalt"), 0, "auth"); err == nil {
		t.Fatal("expected a keeper-side error for iter=0, got nil")
	}
}

// TestPrivsep_KeeperDeath_FailsClosed proves the supervision coupling: after the
// keeper is killed, the front's next call returns a clean error instead of
// hanging. A deadline guards against a hang failing as a timeout rather than a
// blocked test.
func TestPrivsep_KeeperDeath_FailsClosed(t *testing.T) {
	conn, cmd, err := Spawn()
	if err != nil {
		t.Fatalf("Spawn keeper: %v", err)
	}
	defer conn.Close()
	client := newClient(conn)

	// One good round-trip first, so we know the keeper was live.
	if _, err := client.SCRAMProof("h", []byte("saltsalt"), 4096, "auth"); err != nil {
		t.Fatalf("pre-kill SCRAMProof: %v", err)
	}

	// Kill the keeper and reap it via the supervisor, so the peer is definitively
	// gone before the next call.
	done := Supervise(cmd)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill keeper: %v", err)
	}
	<-done

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.SCRAMProof("h", []byte("saltsalt"), 4096, "auth"); err == nil {
		t.Fatal("expected a fail-closed error after keeper death, got a proof")
	}
}
