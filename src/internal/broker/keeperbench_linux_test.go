//go:build linux

package broker

import (
	"context"
	"testing"
	"time"
)

// These benchmarks measure the cost of the Phase-2 two-process split: an in-process
// keeper call (a direct method call) vs the same call crossing the socketpair to a
// REAL keeper subprocess (gob framing + demux + two syscalls + a context switch).
// The delta is the per-op privsep tax; the parallel variant shows the multiplexed
// client amortizes it under concurrency (the gateway's real workload). Run:
//   go test -tags '' -run '^$' -bench BenchmarkKeeper ./src/internal/broker/
// (the TestMain in keeperproc_linux_test.go re-execs the keeper child.)

const benchSecret = "bench-access-token"

// benchInProcKeeper returns an in-process keeper preloaded with one credential +
// handle, plus the handle and credID.
func benchInProcKeeper(b *testing.B) (*localKeeper, string, string) {
	b.Helper()
	k := newLocalKeeper(NewHandles(NewVault()))
	id, err := k.handles.vault.Store(localTenant, Credential{Mode: ModeSubscription, Secret: benchSecret, BaseURL: "https://x"})
	if err != nil {
		b.Fatalf("store: %v", err)
	}
	h, err := k.IssueHandle(id, "box", time.Hour)
	if err != nil {
		b.Fatalf("issue: %v", err)
	}
	return k, h.Value, id
}

// benchTwoProcBroker spawns a real keeper subprocess, preloads one credential +
// handle over the wire, and returns the broker's custody plus the handle and credID.
func benchTwoProcBroker(b *testing.B) (Custody, string, string) {
	b.Helper()
	br, death, err := spawnKeeperBroker("")
	if err != nil {
		b.Fatalf("spawn keeper: %v", err)
	}
	b.Cleanup(func() { br.closeCustody(); <-death })
	id, err := br.Store(Credential{Mode: ModeSubscription, Secret: benchSecret, BaseURL: "https://x"})
	if err != nil {
		b.Fatalf("store: %v", err)
	}
	h, err := br.IssueHandle(id, "box", time.Hour)
	if err != nil {
		b.Fatalf("issue: %v", err)
	}
	return br.custody, h.Value, id
}

// BenchmarkKeeperInjectAuth: the single hottest per-request keeper op.
func BenchmarkKeeperInjectAuth(b *testing.B) {
	ctx := context.Background()
	b.Run("in-process", func(b *testing.B) {
		k, handle, credID := benchInProcKeeper(b)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, _, err := k.InjectAuth(ctx, handle, credID); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("two-process", func(b *testing.B) {
		c, handle, credID := benchTwoProcBroker(b)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, _, err := c.InjectAuth(ctx, handle, credID); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkKeeperRequestPath: the three keeper hops a single brokered HTTP request
// makes — Resolve, InjectAuth, RedactBody — i.e. the per-request privsep tax.
func BenchmarkKeeperRequestPath(b *testing.B) {
	ctx := context.Background()
	body := []byte(`{"model":"x","messages":[{"role":"user","content":"hello world"}]}`)
	run := func(b *testing.B, c Custody, handle string) {
		for i := 0; i < b.N; i++ {
			credID, _, err := c.Resolve(handle)
			if err != nil {
				b.Fatal(err)
			}
			if _, _, err := c.InjectAuth(ctx, handle, credID); err != nil {
				b.Fatal(err)
			}
			c.RedactBody(handle, body)
		}
	}
	b.Run("in-process", func(b *testing.B) {
		k, handle, _ := benchInProcKeeper(b)
		b.ResetTimer()
		run(b, k, handle)
	})
	b.Run("two-process", func(b *testing.B) {
		c, handle, _ := benchTwoProcBroker(b)
		b.ResetTimer()
		run(b, c, handle)
	})
}

// BenchmarkKeeperInjectAuthParallel: concurrent InjectAuth against the two-process
// keeper — the multiplexed client should sustain many in-flight calls, so the
// per-op cost under load is well below the serial round-trip latency.
func BenchmarkKeeperInjectAuthParallel(b *testing.B) {
	ctx := context.Background()
	c, handle, credID := benchTwoProcBroker(b)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, _, err := c.InjectAuth(ctx, handle, credID); err != nil {
				b.Fatal(err)
			}
		}
	})
}
