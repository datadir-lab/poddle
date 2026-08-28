//go:build linux

package broker

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/datadir-lab/poddle/src/internal/privsep"
)

// TestMain re-execs this test binary as the keeper subprocess when spawned with
// PODDLE_PRIVSEP_KEEPER=1 (set by privsep.Spawn): it runs RunKeeperProcess (serving
// a real vault-backed localKeeper over the inherited socketpair) and exits, instead
// of running the suite. In a normal test run the env var is unset, so IsKeeperMode
// is false and every existing broker test runs as usual.
func TestMain(m *testing.M) {
	if privsep.IsKeeperMode() {
		if err := RunKeeperProcess(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestKeeperProcess_TwoProcessBrokerRoundTrip is the production feasibility proof:
// a Broker whose vault lives in a SEPARATE process serves the full custody lifecycle
// — control-plane (Store/IssueHandle/Resolve) AND request-path (InjectAuth) — across
// two real processes over a real socketpair, with the secret existing only in the
// keeper's address space. This is the net.Pipe round-trip (TestBroker_OverKeeperClient)
// re-proven with actual fork/exec, and the SCRAM-spike crossing (PR #152) generalized
// to the real production keeper.
func TestKeeperProcess_TwoProcessBrokerRoundTrip(t *testing.T) {
	br, death, err := spawnKeeperBroker("")
	if err != nil {
		t.Fatalf("spawn keeper broker: %v", err)
	}
	t.Cleanup(func() { br.closeCustody(); <-death })

	// Control-plane across two real processes: store a secret (crosses front->keeper),
	// issue a handle, resolve the full credential back (crosses keeper->front).
	const secret = "two-process-real-secret"
	credID, err := br.Store(Credential{Mode: ModeSubscription, Secret: secret, BaseURL: "https://x"})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	h, err := br.IssueHandle(credID, "box", time.Hour)
	if err != nil {
		t.Fatalf("IssueHandle: %v", err)
	}
	got, err := br.Resolve(h.Value)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Secret != secret {
		t.Errorf("resolved secret = %q, want %q", got.Secret, secret)
	}

	// Request-path across two real processes: InjectAuth returns a mutation that
	// injects the real secret — which lives ONLY in the keeper process — plus a
	// non-secret fingerprint for the front to hold.
	mut, fp, err := br.custody.InjectAuth(context.Background(), h.Value, credID)
	if err != nil {
		t.Fatalf("InjectAuth: %v", err)
	}
	hdr := http.Header{}
	mut.Apply(hdr)
	if hdr.Get("Authorization") != "Bearer "+secret {
		t.Errorf("injected Authorization = %q, want %q", hdr.Get("Authorization"), "Bearer "+secret)
	}
	if fp == "" || fp == secret {
		t.Errorf("fingerprint should be a non-secret digest, got %q", fp)
	}

	// TLS-interception CA across two processes: EnsureCA loads the CA in the KEEPER
	// process (the CA private key never crosses); SignLeaf mints a per-host leaf,
	// and only the leaf cert+key cross back.
	if err := br.custody.EnsureCA(t.TempDir()); err != nil {
		t.Fatalf("EnsureCA across processes: %v", err)
	}
	certDER, keyDER, err := br.custody.SignLeaf("mitm.example")
	if err != nil || len(certDER) == 0 || len(keyDER) == 0 {
		t.Fatalf("SignLeaf across processes: cert=%d key=%d err=%v", len(certDER), len(keyDER), err)
	}

	// The keeper is still alive after the round-trips.
	select {
	case err := <-death:
		t.Fatalf("keeper process died mid-test: %v", err)
	default:
	}
}

// TestNewBrokerFromEnv_TwoProcess proves the opt-in path end to end: with
// PODDLE_BROKER_PRIVSEP=1, NewBrokerFromEnv forks a keeper subprocess and returns a
// non-nil death channel, and the resulting Broker serves the lifecycle across two
// real processes (its vault living only in the keeper).
func TestNewBrokerFromEnv_TwoProcess(t *testing.T) {
	t.Setenv("PODDLE_BROKER_PRIVSEP", "1")
	br, death, err := NewBrokerFromEnv("")
	if err != nil {
		t.Fatalf("NewBrokerFromEnv (two-process): %v", err)
	}
	if death == nil {
		t.Fatal("two-process broker must return a non-nil keeper-death channel")
	}
	t.Cleanup(func() { br.closeCustody(); <-death })

	const secret = "opt-in-two-process-secret"
	credID, err := br.Store(Credential{Mode: ModeSubscription, Secret: secret, BaseURL: "https://x"})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	h, err := br.IssueHandle(credID, "box", time.Hour)
	if err != nil {
		t.Fatalf("IssueHandle: %v", err)
	}
	got, err := br.Resolve(h.Value)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Secret != secret {
		t.Errorf("resolved secret = %q, want %q", got.Secret, secret)
	}
}

// TestKeeperProcess_DeadKeeperFailsClosed proves the front fails closed when the
// keeper process dies: after the client is closed (which the keeper observes as EOF
// and exits), a request-path call errors rather than hanging or proceeding.
func TestKeeperProcess_DeadKeeperFailsClosed(t *testing.T) {
	br, death, err := spawnKeeperBroker("")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	// Seed a credential/handle while the keeper is alive.
	credID, err := br.Store(Credential{Mode: ModeSubscription, Secret: "s", BaseURL: "https://x"})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	h, err := br.IssueHandle(credID, "box", time.Hour)
	if err != nil {
		t.Fatalf("IssueHandle: %v", err)
	}
	// Kill the keeper by closing the client (conn EOF -> keeper exits).
	br.closeCustody()
	<-death

	if _, _, err := br.custody.InjectAuth(context.Background(), h.Value, credID); err == nil {
		t.Error("InjectAuth against a dead keeper process should error (fail closed)")
	}
}
